package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultServerURL = "http://127.0.0.1:8080"

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "asns:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWith(args, stdout, stderr, os.Getenv("ASNS_URL"))
}

func runWith(args []string, stdout, stderr io.Writer, configuredURL string) error {
	command := "help"
	if len(args) > 0 {
		command = args[0]
	}
	baseURL, err := serverURL(configuredURL)
	if err != nil {
		return err
	}

	switch command {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, `ASNS command line

Usage:
  asns help       Show this help
  asns health     Check the local ASNS API
  asns launch     Start ASNS and open its dashboard

Environment:
  ASNS_URL        ASNS base URL (default http://127.0.0.1:8080)`)
		return nil
	case "health":
		if len(args) > 1 {
			return fmt.Errorf("health does not accept arguments; run 'asns help'")
		}
		return checkHealth(context.Background(), baseURL, stdout)
	case "launch":
		if len(args) > 1 {
			return fmt.Errorf("launch does not accept arguments; run 'asns help'")
		}
		if err := launch(context.Background(), baseURL, stdout, stderr); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q; run 'asns help'", command)
	}
}

func serverURL(value string) (string, error) {
	if value == "" {
		return defaultServerURL, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("ASNS_URL must be an http or https base URL without credentials, query, or fragment")
	}
	return strings.TrimRight(value, "/"), nil
}

func checkHealth(ctx context.Context, baseURL string, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/health", nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("ASNS is not reachable at %s: %w", baseURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ASNS health check returned HTTP %d", response.StatusCode)
	}
	var result healthResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode health response: %w", err)
	}
	if result.Status != "ok" || result.Version == "" {
		return errors.New("ASNS returned an invalid health response")
	}
	fmt.Fprintf(stdout, "ASNS is healthy (version %s) at %s\n", result.Version, baseURL)
	return nil
}

func launch(ctx context.Context, baseURL string, stdout, stderr io.Writer) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl was not found; launch ASNS with your service manager or run it manually")
	}
	if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "asns.service").Run(); err != nil {
		start := exec.CommandContext(ctx, "systemctl", "start", "asns.service")
		if output, startErr := start.CombinedOutput(); startErr != nil {
			return fmt.Errorf("start ASNS service: %w: %s; if permission is denied, run 'sudo systemctl start asns.service'", startErr, strings.TrimSpace(string(output)))
		}
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := checkHealth(ctx, baseURL, stdout); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ASNS did not become healthy at %s within 20 seconds", baseURL)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	fmt.Fprintf(stdout, "Dashboard: %s\n", baseURL)
	if _, err := exec.LookPath("xdg-open"); err != nil {
		fmt.Fprintln(stderr, "No desktop browser launcher found; open the dashboard URL manually.")
		return nil
	}
	open := exec.CommandContext(ctx, "xdg-open", baseURL)
	if err := open.Start(); err != nil {
		fmt.Fprintf(stderr, "Could not open a browser automatically: %v\n", err)
	}
	return nil
}
