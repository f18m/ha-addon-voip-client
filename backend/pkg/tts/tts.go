package tts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const ttsUrl = "http://hassio/homeassistant/api/tts_get_url"

type TTSService struct {
	platform string
}

type TTSRequestPayload struct {
	Message  string `json:"message"`
	Platform string `json:"platform"`
}
type TTSResponsePayload struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

func NewTTSService(platform string) *TTSService {
	return &TTSService{
		platform: platform,
	}
}

func (t *TTSService) GetTTSURL(message string) (*TTSResponsePayload, error) {

	hassioToken := os.Getenv("HASSIO_TOKEN")
	if hassioToken == "" {
		return nil, fmt.Errorf("HASSIO_TOKEN environment variable is not set")
	}

	payload := TTSRequestPayload{
		Message:  message,
		Platform: t.platform,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error marshalling payload: %v", err)
	}

	req, err := http.NewRequest("POST", ttsUrl, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+hassioToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error response from TTS service: %s", string(body))
	}

	var responsePayload TTSResponsePayload
	err = json.Unmarshal(body, &responsePayload)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling response: %v", err)
	}
	if responsePayload.URL == "" {
		return nil, fmt.Errorf("TTS service returned empty URL")
	}

	return &responsePayload, nil
}
