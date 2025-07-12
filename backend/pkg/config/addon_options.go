package config

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

// AddonContact provides the contact information for a user
type AddonContact struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

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

	Contacts []AddonContact `json:"contacts"`

	Stats struct {
		Interval string `json:"interval"`
	} `json:"stats"`

	HttpRESTServer struct {
		Synchronous bool `json:"synchronous"`
	} `json:"http_rest_server"`

	VoiceCalls struct {
		MaxDuration string `json:"max_duration"`
	} `json:"voice_calls"`
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

func (o *AddonOptions) GetStatsInterval() time.Duration {
	if o.Stats.Interval == "" {
		return 1 * time.Hour // default value
	}

	// parse the interval string, e.g. "10s", "1m", etc.
	d, err := time.ParseDuration(o.Stats.Interval)
	if err != nil {
		return 1 * time.Hour // default value
	}

	return d
}

func (o *AddonOptions) GetVoiceCallMaxDuration() time.Duration {
	if o.VoiceCalls.MaxDuration == "" {
		return 5 * time.Minute // default value
	}

	// parse the interval string, e.g. "10s", "1m", etc.
	d, err := time.ParseDuration(o.VoiceCalls.MaxDuration)
	if err != nil {
		return 5 * time.Minute // default value
	}

	return d
}
