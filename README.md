# Drive Assets to Google Cloud Storage

This is a command-line tool that, given a Drive folder, lists specific mime-type files (.png & .jpg by default) within the Drive folder, downloads them locally, uploads them to a designated Google Cloud Storage bucket, and describes them with Gemini.

## Installation

If you have [Go](https://go.dev/) installed, you can install this tool like this:

```
go install github.com/ghchinoy/drivetogcs@latest
```

### From Source

Clone the git repo and build with [Go](https://go.dev/):

```
go build
```

## Prerequisites

Two environment variables are necessary

* `PROJECT_ID` - Google Cloud Project ID; e.g. `export PROJECT_ID=$(gcloud config get project)`
* `GOOGLE_CREDENTIALS` - path to Google Project OAuth2 credentials, used for accessing Drive, see below for instructions

### Google Cloud Credentials
To obtain an OAuth 2.0 Client ID, go to your Google Cloud Console and to the API & Services > Credentials page to Create Credentials for an OAuth client ID that's a Desktop application type. 

This credential will be used to open a web page authorization and then store a temporary token locally so that the application can access Drive on your behalf.

Store the JSON locally. Set the `GOOGLE_CREDENTIALS` environment variable to the path to the JSON. **DO NOT** check this file in to any code repository - keep it private. 

Documentation is [here](https://developers.google.com/identity/protocols/oauth2) and [here](https://support.google.com/cloud/answer/15549257?hl=en).



## Example usage

```
export PROJECT_ID=$(gcloud config get project)
export GOOGLE_CREDENTIALS=PATH_TO_JSON

go run *.go --folder 1bnr_UFzNpTTagFUGc8t9EIbpCi6QHe-j --gcs-bucket my-bucket --gcs-path vto/garments
```

## Flags

* `folder`: required, the Google Drive Folder ID
* `mime-types`: optional, a comma-separated list of the mime-types to retrieve from Drive, defaults to "image/jpeg,image/png"
* `local`: optional, the local folder name to store downloaded drive files, defaults to `local`
* `max`: optional, maximum files to process, useful for processing a small batch
* `gcs-bucket`: optional, the target Google Cloud Storage bucket, it defaults to gs://$PROJECT_ID-media
* `gcs-path`: optional, the folder within the Google Cloud Storage bucket; if used, this should not begin with a `/`
* `always-upload`: optional, uploads the file to Google Cloud Storage, regardless of whether it exists in the target bucket; the default is false: it'll check if the file exists and skip uploading
* `description`: optional, defaults to `true` - describes the media with Gemini
* `prompt`: optional, prompt template to use; by default, it uses the built in prompt template that describes the media downloaded
