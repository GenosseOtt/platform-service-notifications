package main

import (
	"fmt"
	"os"

	"github.com/openmcp-project/platform-service-notifications/cmd/platform-service-notifications/app"
)

func main() {
	cmd := app.NewPlatformServiceCommand()

	if err := cmd.Execute(); err != nil {
		fmt.Print(err)
		os.Exit(1)
	}
}
