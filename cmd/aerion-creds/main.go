// aerion-creds outputs OAuth credentials as JSON.
// Built with ldflags in CI, shipped alongside the Flatpak build so the main
// app (built from source on Flathub) can read credentials at runtime.
//
// Build:
//   go build -ldflags "-X 'main.GoogleClientID=...' -X 'main.GoogleClientSecret=...' -X 'main.MicrosoftClientID=...' -X 'main.GoogleTestingClientID=...' -X 'main.GoogleTestingClientSecret=...'" -o aerion-creds
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

var (
	GoogleClientID            string
	GoogleClientSecret        string
	MicrosoftClientID         string
	GoogleTestingClientID     string
	GoogleTestingClientSecret string
)

func main() {
	creds := map[string]string{
		"google_client_id":             GoogleClientID,
		"google_client_secret":         GoogleClientSecret,
		"microsoft_client_id":          MicrosoftClientID,
		"google_testing_client_id":     GoogleTestingClientID,
		"google_testing_client_secret": GoogleTestingClientSecret,
	}
	data, err := json.Marshal(creds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal credentials: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(data))
}
