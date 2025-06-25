package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"voip-client-backend/pkg/config"
	"voip-client-backend/pkg/logger"
)

type Payload struct {
	CalledNumber  string `json:"called_number"`
	CalledContact string `json:"called_contact"`
	MessageTTS    string `json:"message_tts"`
}

type HttpServer struct {
	logger           *logger.CustomLogger
	server           *http.Server
	contactLookupMap map[string]string // Maps contact names to their URIs
	outCh            chan Payload
}

const logPrefix = "httpserver"

func NewServer(logger *logger.CustomLogger, contacts []config.AddonContact) HttpServer {
	h := HttpServer{
		logger:           logger,
		outCh:            make(chan Payload),
		contactLookupMap: make(map[string]string),
	}

	// convert slice to map:
	for _, contact := range contacts {
		h.contactLookupMap[contact.Name] = contact.URI
		h.logger.InfoPkgf(logPrefix, "Contact %s added with URI %s", contact.Name, contact.URI)
	}

	// Use the http.NewServeMux() function to create an empty servemux.
	mux := http.NewServeMux()

	// Define the handler for the HTTP endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only accept POST requests
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
			return
		}

		// Decode the JSON payload from the request body
		var payload Payload
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Log the received payload
		logger.InfoPkgf(logPrefix, "Received payload: CalledNumber=%s, MessageTTS=%s\n", payload.CalledNumber, payload.MessageTTS)

		// Validate it
		if payload.CalledNumber == "" && payload.CalledContact == "" {
			http.Error(w, "CalledNumber or CalledContact are required", http.StatusBadRequest)
			return
		}
		if payload.CalledNumber != "" && payload.CalledContact != "" {
			http.Error(w, "Only one between CalledNumber and CalledContact can be provided", http.StatusBadRequest)
			return
		}
		if payload.MessageTTS == "" {
			http.Error(w, "MessageTTS is required", http.StatusBadRequest)
			return
		}

		if payload.CalledNumber != "" {
			pattern := `^sip:[^@]+@[^@]+\.[^@]+$`
			valid, err := regexp.MatchString(pattern, payload.CalledNumber)
			if err != nil {
				http.Error(w, "Error validating CalledNumber", http.StatusInternalServerError)
				return
			}
			if !valid {
				http.Error(w, "CalledNumber must be in the format sip:<number>@<domain>", http.StatusBadRequest)
				return
			}
		} else if payload.CalledContact != "" {
			// Check if we know about this contact
			contactURI, exists := h.contactLookupMap[payload.CalledContact]
			if !exists {
				http.Error(w, fmt.Sprintf("Unknown contact: %s", payload.CalledContact), http.StatusBadRequest)
				return
			}

			payload.CalledNumber = contactURI // Use the contact URI as the CalledNumber
			logger.InfoPkgf(logPrefix, "Using contact URI %s for CalledContact %s", payload.CalledNumber, payload.CalledContact)
		}

		// Respond to the client
		// TODO: handle synchronous response
		// w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Payload is valid. Initiating TTS generation and outgoing call.")

		// Send to the output channel
		h.outCh <- payload
	})

	// Create a custom HTTP server with timeouts
	h.server = &http.Server{
		Addr:           ":80", // Address to listen on -- this is fixed to the default HTTP port
		Handler:        mux,
		ReadTimeout:    10 * time.Second,  // Maximum duration for reading the entire request, including body
		WriteTimeout:   10 * time.Second,  // Maximum duration before timing out writes of the response
		IdleTimeout:    120 * time.Second, // Maximum amount of time to wait for the next request when keep-alive is enabled
		MaxHeaderBytes: 1 << 18,           // Max size of request headers, default is 256kB
	}

	return h
}

func (h *HttpServer) ListenAndServe() {
	h.logger.InfoPkgf(logPrefix, "Server listening on %s", h.server.Addr)
	if err := h.server.ListenAndServe(); err != nil {
		h.logger.Fatalf("Failed to start server: %s", err)
	}
}

func (h *HttpServer) GetInputChannel() chan Payload {
	return h.outCh
}
