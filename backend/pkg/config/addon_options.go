package config

import (
	"encoding/json"
	"io"
	"os"
)

// AddonOptions contains the configuration provided by the user to the Home Assistant addon
// in the HomeAssistant YAML editor
type AddonOptions struct {
	VoipProvider struct {
		Name     string `json:"name"`
		Account  string `json:"account"`
		Password string `json:"password"`
	} `json:"voip_provider"`

	TTSEngine struct {
		Platform string `json:"platform"`
	} `json:"tts_engine"`

	Contacts []struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	} `json:"contacts"`
}

// readAddonOptions reads the OPTIONS of this Home Assistant addon
func ReadAddonOptions() (*AddonOptions, error) {
	optionFile, errOpen := os.Open(defaultHomeAssistantOptionsFile)
	if errOpen != nil {
		return nil, errOpen
	}
	defer func() {
		_ = optionFile.Close()
	}()

	// read whole file
	data, err := io.ReadAll(optionFile)
	if err != nil {
		return nil, err
	}

	// JSON parse
	o := AddonOptions{}
	err = json.Unmarshal(data, &o)
	if err != nil {
		return nil, err
	}

	return &o, nil
}
