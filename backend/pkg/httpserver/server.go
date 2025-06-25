package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"voip-client-backend/pkg/config"
	"voip-client-backend/pkg/fsm"
	"voip-client-backend/pkg/logger"
)

type DialPayload struct {
	CalledNumber  string `json:"called_number"`
	CalledContact string `json:"called_contact"`
	MessageTTS    string `json:"message_tts"`
}

type HttpServer struct {
	logger           *logger.CustomLogger
	server           *http.Server
	contactLookupMap map[string]string // Maps contact names to their URIs
	synchronous      bool

	fsmStateCh chan fsm.FSMState
	outCh      chan DialPayload
}

const logPrefix = "httpserver"
const dialEndpoint = "/dial"

func NewServer(logger *logger.CustomLogger, sync bool, contacts []config.AddonContact) HttpServer {
	h := HttpServer{
		logger:           logger,
		synchronous:      sync,
		outCh:            make(chan DialPayload),
		contactLookupMap: make(map[string]string),
	}

	// convert slice to map:
	for _, contact := range contacts {
		h.contactLookupMap[contact.Name] = contact.URI
		h.logger.InfoPkgf(logPrefix, "Contact %s added with URI %s", contact.Name, contact.URI)
	}

	// Use the http.NewServeMux() function to create an empty servemux.
	mux := http.NewServeMux()

	// Define the handler for each HTTP endpoint
	mux.HandleFunc(dialEndpoint, func(w http.ResponseWriter, r *http.Request) {
		h.serveDial(w, r)
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

func (h *HttpServer) SetFSMChannel(c chan fsm.FSMState) {
	h.fsmStateCh = c
}

func (h *HttpServer) waitForFSMState(s fsm.FSMState) {
	for {
		// Wait for a FSM state change
		state := <-h.fsmStateCh

		// Is it the state we are waiting for?
		if state == s {
			// yes
			h.logger.InfoPkgf(logPrefix, "FSM state changed to %s", s)
			return
		}

		// keep waiting
		h.logger.InfoPkgf(logPrefix, "Ignoring FSM state change: %s", state)
	}
}

func (h *HttpServer) serveDial(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 405: Only POST method is allowed")
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Decode the JSON payload from the request body
	var payload DialPayload
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: invalid JSON payload")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Log the received payload
	h.logger.InfoPkgf(logPrefix, "Received payload: CalledNumber=%s, CalledContact=%s, MessageTTS=%s\n",
		payload.CalledNumber, payload.CalledContact, payload.MessageTTS)

	// Validate it
	if payload.CalledNumber == "" && payload.CalledContact == "" {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: CalledNumber or CalledContact is required")
		http.Error(w, "CalledNumber or CalledContact is required", http.StatusBadRequest)
		return
	}
	if payload.CalledNumber != "" && payload.CalledContact != "" {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: Only one between CalledNumber and CalledContact can be provided")
		http.Error(w, "Only one between CalledNumber and CalledContact can be provided", http.StatusBadRequest)
		return
	}
	if payload.MessageTTS == "" {
		h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: MessageTTS is required")
		http.Error(w, "MessageTTS is required", http.StatusBadRequest)
		return
	}

	if payload.CalledNumber != "" {
		pattern := `^sip:[^@]+@[^@]+\.[^@]+$`
		valid, err := regexp.MatchString(pattern, payload.CalledNumber)
		if err != nil {
			h.logger.InfoPkg(logPrefix, "Replying with HTTP 500: Error validating CalledNumber")
			http.Error(w, "Error validating CalledNumber", http.StatusInternalServerError)
			return
		}
		if !valid {
			h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: CalledNumber must be in the format sip:<number>@<domain>")
			http.Error(w, "CalledNumber must be in the format sip:<number>@<domain>", http.StatusBadRequest)
			return
		}
	} else if payload.CalledContact != "" {
		// Check if we know about this contact
		contactURI, exists := h.contactLookupMap[payload.CalledContact]
		if !exists {
			h.logger.InfoPkg(logPrefix, "Replying with HTTP 400: Unknown contact")
			http.Error(w, fmt.Sprintf("Unknown contact: %s", payload.CalledContact), http.StatusBadRequest)
			return
		}

		payload.CalledNumber = contactURI // Use the contact URI as the CalledNumber
		h.logger.InfoPkgf(logPrefix, "Using contact URI %s for CalledContact %s", payload.CalledNumber, payload.CalledContact)
	}

	if !h.synchronous {
		// Respond to the client immediately
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Payload is valid. Initiating TTS generation and outgoing call.")
		h.logger.InfoPkgf(logPrefix, "Replying with HTTP 200: Payload is valid. Initiating TTS generation and outgoing call.")
	}

	// Send to the output channel
	h.outCh <- payload

	if h.synchronous {

		// wait till the FSM goes back into WaitingInputs state
		h.waitForFSMState(fsm.WaitingInputs)

		// then respond to the client
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Payload is valid. Initiating TTS generation and outgoing call.")
		h.logger.InfoPkgf(logPrefix, "Replying with HTTP 200: Payload is valid. Initiating TTS generation and outgoing call.")
	}
}

func (h *HttpServer) ListenAndServe() {
	h.logger.InfoPkgf(logPrefix, "Server listening on %s, paths: %s", h.server.Addr, dialEndpoint)
	if err := h.server.ListenAndServe(); err != nil {
		h.logger.Fatalf("Failed to start server: %s", err)
	}
}

func (h *HttpServer) GetInputChannel() chan DialPayload {
	return h.outCh
}
