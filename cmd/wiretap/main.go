package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	host := flag.String("host", "127.0.0.1", "Asterisk AMI host")
	port := flag.Int("port", 5038, "Asterisk AMI port")
	user := flag.String("user", "admin", "AMI username")
	secret := flag.String("secret", "", "AMI secret")
	outDir := flag.String("outdir", "testdata/captures", "Output directory for captures")
	sanitize := flag.String("sanitize", "", "Sanitize a capture file in-place (keeps .bak)")
	flag.Parse()

	if *sanitize != "" {
		if err := sanitizeFile(*sanitize); err != nil {
			fmt.Fprintf(os.Stderr, "sanitize error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("sanitized:", *sanitize)
		return
	}

	if *secret == "" {
		fmt.Fprintln(os.Stderr, "error: -secret is required")
		flag.Usage()
		os.Exit(1)
	}

	if err := capture(*host, *port, *user, *secret, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func capture(host string, port int, user, secret, outDir string) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	fmt.Printf("connecting to %s...\n", addr)

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	filename := filepath.Join(outDir, time.Now().Format("20060102-150405")+".raw")
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	fmt.Printf("writing to %s\n", filename)

	// Read the banner
	reader := bufio.NewReader(conn)
	banner, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading banner: %w", err)
	}
	f.WriteString(banner)
	fmt.Printf("banner: %s", banner)

	// Send login
	loginCmd := fmt.Sprintf("Action: Login\r\nUsername: %s\r\nSecret: %s\r\n\r\n", user, secret)
	if _, err := conn.Write([]byte(loginCmd)); err != nil {
		return fmt.Errorf("sending login: %w", err)
	}

	// Stream everything to file
	fmt.Println("streaming events (ctrl+c to stop)...")
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		f.WriteString(line + "\n")
	}

	return scanner.Err()
}

var (
	ipPattern       = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	phonePattern    = regexp.MustCompile(`\b1?\d{10}\b`)
	secretPattern   = regexp.MustCompile(`(?i)(Secret:\s*).+`)
	passwordPattern = regexp.MustCompile(`(?i)(Password:\s*).+`)
)

func sanitizeFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Create backup
	bakPath := path + ".bak"
	if err := os.WriteFile(bakPath, data, 0o644); err != nil {
		return fmt.Errorf("creating backup: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		// Redact secrets/passwords
		line = secretPattern.ReplaceAllString(line, "${1}REDACTED")
		line = passwordPattern.ReplaceAllString(line, "${1}REDACTED")

		// Redact IPs (but preserve localhost)
		line = ipPattern.ReplaceAllStringFunc(line, func(ip string) string {
			if ip == "127.0.0.1" {
				return ip
			}
			return "10.0.0.1"
		})

		// Redact phone numbers in CallerID fields
		if strings.Contains(line, "CallerID") || strings.Contains(line, "ConnectedLine") {
			line = phonePattern.ReplaceAllString(line, "15550001234")
		}

		lines[i] = line
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
