package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/skratchdot/open-golang/open"
	"golang.org/x/oauth2"
)

// Retrieve a token, saves the token, then returns the generated client.
func getClient(config *oauth2.Config, manualAuth bool) *http.Client {
	// The file token.json stores the user's access and refresh tokens, and is
	// created automatically when the authorization flow completes for the first
	// time.
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		if !manualAuth {
			tok = getTokenFromWebLaunch(config)
			saveToken(tokFile, tok)
		} else {
			tok = getTokenFromWeb(config)
			saveToken(tokFile, tok)
		}
	}
	return config.Client(context.Background(), tok)
}

// getTokenFromWebLaunch retrieves an exchanged OAuth2 token after launching a web browser
func getTokenFromWebLaunch(config *oauth2.Config) *oauth2.Token {

	config.RedirectURL = "http://localhost:8080"

	// Redirect user to Google's consent page to ask for permission
	// for the scopes specified above.
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)

	// obtain the token from oauth flow
	log.Println(color.CyanString("You will now be taken to your browser for authentication"))
	time.Sleep(1 * time.Second)
	err := open.Run(authURL)
	if err != nil {
		log.Fatalf("unable to open browser: %v", err)
	}
	time.Sleep(1 * time.Second)
	log.Printf("Authentication URL: %s\n", authURL)

	var code string

	errorChan := make(chan error)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		authCode := r.URL.Query().Get("code")
		// Use the authorization code that is pushed to the redirect URL.
		if authCode != "" {
			code = authCode
			w.Write([]byte("Authentication successful. You may close this browser window.\n"))

			errorChan <- nil
			return
		}
		//log.Fatal("No code in exchange")
		errorChan <- fmt.Errorf("no code in exchange")
	})
	go func() {
		log.Printf("listening on %s", ":8080")
		err := http.ListenAndServe("localhost:8080", nil)
		if err != nil {
			log.Fatal(err)
		}
	}()
	err = <-errorChan
	if err != nil {
		log.Fatalf("received an error while listening for token: %v", err)
	}

	// Handle the exchange code to initiate a transport.
	tok, err := config.Exchange(context.TODO(), code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	log.Println(color.CyanString("Authentication successful"))
	return tok
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}
