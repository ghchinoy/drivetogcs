package main

import (
	"bytes"
	"context"
	"embed"
	_ "embed"
	"encoding/csv"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/genai"
)

var sourceFolderID string
var localFolderName string = "local"
var maxFiles int

var projectID string
var location string = "us-central1"
var model string = "gemini-2.0-flash"

var gcsBucket string
var gcsFolderPath string
var alwaysUploadToGCS bool

var createDescription bool
var customPromptLocation string

var mimeTypes []string

//go:embed prompts/*.tpl
var promptTemplates embed.FS

var (
	driveSrv    *drive.Service
	genaiClient *genai.Client
)

func init() {
	flag.StringVar(&sourceFolderID, "folder", sourceFolderID, "source Drive folder ID")
	flag.StringVar(&localFolderName, "local", localFolderName, "local folder name")
	flag.IntVar(&maxFiles, "max", maxFiles, "max files to process, useful for processing a small batch")

	flag.StringVar(&gcsBucket, "gcs-bucket", "", "GCS bucket")
	flag.StringVar(&gcsFolderPath, "gcs-path", "", "GCS path")
	flag.BoolVar(&alwaysUploadToGCS, "always-upload", false, "always upload to GCS")

	flag.BoolVar(&createDescription, "describe", true, "describe the asset using Gemini")
	flag.StringVar(&customPromptLocation, "prompt", "", "a custom prompt template to use")

	mimeTypesFlag := flag.String("mime-types", "image/jpeg,image/png", "Comma-separated list of MIME types")
	mimeTypes = strings.Split(*mimeTypesFlag, ",")

	flag.Parse()
}

func main() {
	// prerequisites
	// Get the Project ID from the environment
	projectID = os.Getenv("PROJECT_ID")
	if projectID == "" {
		log.Fatalf("Please provide PROJECT_ID environment variable, e.g. export PROJECT_ID=$(gcloud config get-value core/project)")
	}
	// Get the Google Cloud region location from the environment
	location = os.Getenv("LOCATION")
	if location == "" {
		location = "us-central1"
	}

	// Get the Google credentials from the environment variable
	credentials := os.Getenv("GOOGLE_CREDENTIALS")
	if credentials == "" {
		panic("GOOGLE_CREDENTIALS not set")
	}

	// other guards
	// set target GCS bucket as gs://PROJECT_ID-media
	if gcsBucket == "" {
		gcsBucket = fmt.Sprintf("%s-media", projectID)
	}

	ctx := context.Background()

	// Initialize Drive Service
	b, err := os.ReadFile(credentials)
	if err != nil {
		panic(err)
	}
	config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/drive")
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(config)

	driveSrv, err = drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to create Drive service: %v", err)
	}

	// Initialize genai Client
	genaiClient, err = createGenaiClient(ctx)
	if err != nil {
		log.Fatalf("Unable to create genai client: %v", err)
	}

	//mimeTypes := []string{"image/jpeg", "image/png", "image/webp"}
	fileList := listFiles(ctx, sourceFolderID, mimeTypes)
	log.Printf("Files %d", len(fileList))

	var wg sync.WaitGroup

	csvFile, err := os.Create("descriptions.csv")
	if err != nil {
		log.Fatalf("failed to create CSV file: %v", err)
	}
	defer csvFile.Close()

	csvWriter := csv.NewWriter(csvFile)
	defer csvWriter.Flush() // Ensure all buffered data is written

	fileCount := len(fileList)
	if maxFiles > 0 && maxFiles < fileCount {
		fileCount = maxFiles
	}

	for i := 0; i < fileCount; i++ {
		file := fileList[i]
		wg.Add(1)
		go func(file drive.File) {
			defer wg.Done()
			description, size, err := describe(ctx, file)
			if err != nil {
				description = fmt.Sprintf("Error: %v", err) // Store error in description
			}
			record := []string{
				file.Name,
				fmt.Sprintf("%d", size),
				file.MimeType,
				file.Id,
				description,
			}
			if err := csvWriter.Write(record); err != nil {
				log.Printf("failed to write to CSV: %v", err)
			}

			if err != nil {
				log.Printf("unable to describe: %v", err)
			}
			log.Printf("%s (%s) %s = %s", file.Name, file.MimeType, file.Id, description)
		}(file)
	}
	wg.Wait()

	log.Println("CSV file written successfully.")
}

// listFiles lists all the files in a Drive folder
func listFiles(ctx context.Context, folderID string, mimeTypes []string) []drive.File {
	// ref https://developers.google.com/drive/api/guides/search-files
	//query := "mimeType = 'image/jpeg'"
	//query := "name contains '.jpg'"
	//query := fmt.Sprintf("'%s' in parents and mimeType contains 'image' and (name contains '.jpg' or name contains '.png')", folderID)

	// Build the mimeType portion of the query.
	mimeQueryParts := make([]string, len(mimeTypes))
	for i, mimeType := range mimeTypes {
		mimeQueryParts[i] = fmt.Sprintf("mimeType = '%s'", mimeType)
	}
	mimeQuery := strings.Join(mimeQueryParts, " or ")

	// Build the full query.
	query := fmt.Sprintf("'%s' in parents and (%s)", folderID, mimeQuery)

	fileList, err := driveSrv.Files.List().
		PageSize(1000).
		Q(query).
		Do()
	if err != nil {
		log.Fatalf("error occurred while listing files: %v", err)
	}
	log.Printf("%s has %d files matching %s", folderID, len(fileList.Files), query)

	found := []drive.File{}
	for _, f := range fileList.Files {
		if f != nil {
			found = append(found, *f)
		}
	}
	return found
}

// describe describes an image given an image file from drive
func describe(ctx context.Context, imageFile drive.File) (string, int, error) {
	// obtain file
	fileBytes, err := getFileBytes(imageFile)
	if err != nil {
		return "", 0, err
	}
	log.Printf("Obtained file bytes %s (%d)", imageFile.Name, len(fileBytes))

	// upload file to Google Cloud Storage
	err = uploadFileToGCS(ctx, gcsBucket, gcsFolderPath, imageFile.Name, fileBytes, alwaysUploadToGCS)
	if err != nil {
		log.Printf("Unable to upload to GCS")
	}
	byteCount := len(fileBytes)

	// Describe using Gemini multimodal
	var descriptionText string

	if createDescription {
		log.Printf("Describing %s ...", imageFile.Name)

		var tmpl *template.Template

		if customPromptLocation != "" {
			var err error
			tmpl, err = template.ParseFiles(customPromptLocation)
			if err != nil {
				return "", 0, fmt.Errorf("failed to parse custom template: %w", err)
			}
		} else {
			tmpl = template.Must(
				template.New("describe_media.tpl").ParseFS(promptTemplates, "prompts/describe_media.tpl"),
			)
		}
		data := struct {
			ImageName string
		}{
			imageFile.Name,
		}
		buf := new(bytes.Buffer)
		err = tmpl.Execute(buf, data)
		if err != nil {
			return "", 0, err
		}
		prompt := buf.String()

		contents := []*genai.Content{}
		contents = append(contents, genai.NewUserContentFromBytes(fileBytes, imageFile.MimeType))
		contents = append(contents, genai.Text(prompt)...)

		config := &genai.GenerateContentConfig{}
		description, err := genaiClient.Models.GenerateContent(
			ctx, model,
			contents,
			config,
		)
		if err != nil {
			log.Printf("unable to generate content: %v", err)
			log.Printf("prompt: %s", prompt)
			return "", 0, nil
		}
		descriptionText = description.Text()
	} else {
		descriptionText = "Description skipped"
	}

	return descriptionText, byteCount, nil
}

// getFileBytes retrieves a file from Drive
func getFileBytes(file drive.File) ([]byte, error) {
	//ctx := context.Background()

	// Download the file
	call := driveSrv.Files.Get(file.Id)

	resp, err := call.Download()
	if err != nil {
		log.Fatalf("Error downloading file: %v", err)
	}
	defer resp.Body.Close()

	// Check the response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Error: HTTP status code %d", resp.StatusCode)
	}

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, resp.Body)

	if err != nil {
		return nil, fmt.Errorf("Unable to read response body: %v", err)
	}
	fileBytes := buf.Bytes()

	// Create the local folder if it doesn't exist.
	if _, err := os.Stat(localFolderName); os.IsNotExist(err) {
		if err := os.MkdirAll(localFolderName, 0755); err != nil { // Use MkdirAll for nested dirs
			return nil, fmt.Errorf("Unable to create local folder: %v", err)
		}
	}

	localFilePath := filepath.Join(localFolderName, file.Name) // Construct the full local file path.

	// Write the bytes to a file with the same name, but only if it doesn't already exist
	if _, err := os.Stat(localFilePath); os.IsNotExist(err) {
		log.Printf("writing %s ...", file.Name)
		err = os.WriteFile(localFilePath, fileBytes, 0644)
		if err != nil {
			return nil, fmt.Errorf("unable to write file: %v", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("error checking if file exists: %v", err)
	} else {
		log.Printf("File '%s' exists locally, skipping write.", localFilePath)
	}

	return fileBytes, nil
}

// uploadFileToGCS uploads a byte slice to a Google Cloud Storage bucket and folder path.
func uploadFileToGCS(ctx context.Context, bucketName, folderPath, objectName string, fileBytes []byte, override bool) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}
	defer client.Close()

	objectPath := filepath.Join(folderPath, objectName) // Construct the full object path

	// Check if the object already exists
	if !override {
		_, err = client.Bucket(bucketName).Object(objectPath).Attrs(ctx)
		if err == nil {
			log.Printf("File '%s' already exists in GCS %s. Skipping upload.\n", objectPath, bucketName)
			return nil // Object exists, return nil error
		} else if err != storage.ErrObjectNotExist {
			return fmt.Errorf("failed to check object existence: %v", err) // Unexpected error
		}
	}

	wc := client.Bucket(bucketName).Object(objectPath).NewWriter(ctx)
	if _, err = wc.Write(fileBytes); err != nil {
		return fmt.Errorf("failed to write file to GCS: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %v", err)
	}
	log.Printf("uploaded to %s/%s", bucketName, objectPath)

	return nil
}

// createGenaiClient Creates a Google Generative AI client for use
func createGenaiClient(ctx context.Context) (*genai.Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  projectID,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		log.Printf("failed to create client: %v", err)
		return nil, err
	}
	return client, nil
}
