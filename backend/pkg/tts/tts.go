package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"voip-client-backend/pkg/logger"
)

const ttsUrl = "http://hassio/homeassistant/api/tts_get_url"
const ttsDlPath = "/share/voip-client"
const ttsHttpApiTimeout = 10 * time.Second
const logPrefix = "tts"

type TTSService struct {
	logger   *logger.CustomLogger
	platform string
}

// see https://www.home-assistant.io/integrations/tts/#rest-api
// and https://www.home-assistant.io/integrations/google_translate/
type haTTSOptions struct {
	PreferredFormat         string `json:"preferred_format"`
	PreferredSampleRate     string `json:"preferred_sample_rate"`
	PreferredSampleChannels string `json:"preferred_sample_channels"`
	PreferredSampleBytes    string `json:"preferred_sample_bytes"`
}
type haTTSRequestPayload struct {
	Message  string       `json:"message"`
	Platform string       `json:"platform"`
	Options  haTTSOptions `json:"options"`
}
type haTTSResponsePayload struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

func NewTTSService(logger *logger.CustomLogger, platform string) *TTSService {
	return &TTSService{
		logger:   logger,
		platform: platform,
	}
}

func (t *TTSService) getTTSURL(message string) (*haTTSResponsePayload, error) {

	hassioToken := os.Getenv("HASSIO_TOKEN")
	if hassioToken == "" {
		return nil, fmt.Errorf("HASSIO_TOKEN environment variable is not set")
	}

	payload := haTTSRequestPayload{
		Message:  message,
		Platform: t.platform,

		// The TTS options are dictated by Baresip which supports (via the "aufile" module)
		// only the following specifications: monochannel, 8kHz, 16bit WAV
		// If we had to do the conversion ourselves, using ffmpeg CLI utility it would be:
		//  ffmpeg -i input.wav -ac 1 -ar 8000 -acodec pcm_s16le baresip-audio.wav
		Options: haTTSOptions{
			PreferredFormat:         "wav",
			PreferredSampleRate:     "8000",
			PreferredSampleChannels: "1", // monochannel
			PreferredSampleBytes:    "2", // 16bit audio sampling
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error marshalling payload: %w", err)
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), ttsHttpApiTimeout)
	defer cancelFn()

	t.logger.InfoPkgf(logPrefix, "Launching HTTP POST to the HomeAssistant TTS [%s] with payload [%s]", ttsUrl, payloadBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ttsUrl, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+hassioToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error response from TTS service: %s", string(body))
	}

	var responsePayload haTTSResponsePayload
	err = json.Unmarshal(body, &responsePayload)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling response: %w", err)
	}
	if responsePayload.URL == "" {
		return nil, fmt.Errorf("TTS service returned empty URL")
	}

	return &responsePayload, nil
}

func (t *TTSService) getOutputFilepath(message string) string {
	// Hash with sha256 the message to create a unique filename:
	hasher := sha256.New()
	hasher.Write([]byte(message))
	hash := hex.EncodeToString(hasher.Sum(nil))
	return filepath.Join(ttsDlPath, "tts_"+hash+".wav")
}

func (t *TTSService) downloadAudioFile(url string, outPath string) error {
	// Create a custom HTTP client with timeouts
	client := &http.Client{
		Timeout: ttsHttpApiTimeout,
	}

	// Create a new request with context
	t.logger.InfoPkgf(logPrefix, "Launching HTTP GET to the HomeAssistant TTS to retrieve audio file [%s]", url)
	ctx, cancel := context.WithTimeout(context.Background(), ttsHttpApiTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	// Get the data
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Create the file
	out, err := os.Create(outPath) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

func (t *TTSService) GetAudioFile(message string) (string, error) {

	// Support local testing outside HomeAssistant environment
	localTesting := os.Getenv("LOCAL_TESTING") != ""
	if localTesting {
		t.logger.InfoPkgf(logPrefix, "Running in local testing mode, using hardcoded audio file instead of TTS service")
		return "/usr/share/baresip/test-message.wav", nil // return a hardcoded path
	}

	// Prepare the output file path
	outPath := t.getOutputFilepath(message)
	if _, err := os.Stat(outPath); err == nil {
		// the result of TTS engine has been cached...
		t.logger.InfoPkgf(logPrefix, "Audio file for message [%s] already exists at [%s], skipping TTS service call", message, outPath)
		return outPath, nil
	}

	// Prepare output directory
	if err := os.MkdirAll(ttsDlPath, 0755); err != nil {
		return "", fmt.Errorf("error creating directory %s: %w", ttsDlPath, err)
	}

	// Get the TTS URL
	responsePayload, err := t.getTTSURL(message)
	if err != nil {
		return "", fmt.Errorf("error getting TTS URL: %w", err)
	}

	// Download the audio file
	err = t.downloadAudioFile(responsePayload.URL, outPath)
	if err != nil {
		return "", fmt.Errorf("error downloading audio file: %w", err)
	}

	t.logger.InfoPkgf(logPrefix, "Successfully retrieved audio file and stored at [%s]", outPath)

	return outPath, nil // return the path to the downloaded file
}
