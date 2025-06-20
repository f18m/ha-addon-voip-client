// Package main is the entry point for the DHCP clients web application backend.
package main

import (
	"voip-client-backend/pkg/logger"
)

func main() {
	logger := logger.NewCustomLogger("voip-client")

	logger.Info("VOIP client backend starting")

}
