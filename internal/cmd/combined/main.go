// Package main provides a unified entry point for Beszel.
//
// Usage:
//
//	./beszel                 - Self-monitoring: hub + local agent, no login
//	./beszel -auth user:pass - Self-monitoring with password protection
//	./beszel -hub            - Hub-only mode (web dashboard for remote agents)
//	./beszel -agent          - Agent-only mode (monitored server)
package main

import (
	"crypto/ed25519"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/henrygd/beszel"
	"github.com/henrygd/beszel/agent"
	"github.com/henrygd/beszel/agent/health"
	"github.com/henrygd/beszel/agent/utils"
	"github.com/henrygd/beszel/internal/hub"
	_ "github.com/henrygd/beszel/internal/migrations"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	gossh "golang.org/x/crypto/ssh"
)

// selfModeConfig holds self-mode configuration
type selfModeConfig struct {
	authUser   string // empty = no auth required (auto-login)
	authPass   string
	socketPath string
}

var selfCfg selfModeConfig

func main() {
	args := os.Args[1:]
	mode := "self"

	if len(args) > 0 {
		switch args[0] {
		case "-hub", "--hub":
			mode = "hub"
			os.Args = append(os.Args[:1], os.Args[2:]...)
		case "-agent", "--agent":
			mode = "agent"
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	switch mode {
	case "hub":
		runHub()
	case "agent":
		runAgent()
	case "self":
		parseSelfArgs()
		runSelf()
	}
}

// parseSelfArgs strips -auth user:pass from os.Args and saves it
func parseSelfArgs() {
	selfCfg.socketPath = "/tmp/beszel-self.sock"

	var newArgs []string
	i := 0
	for i < len(os.Args) {
		arg := os.Args[i]
		var val string
		consumed := false

		if (arg == "-auth" || arg == "--auth") && i+1 < len(os.Args) {
			val = os.Args[i+1]
			i += 2
			consumed = true
		} else if strings.HasPrefix(arg, "-auth=") {
			val = strings.TrimPrefix(arg, "-auth=")
			i++
			consumed = true
		} else if strings.HasPrefix(arg, "--auth=") {
			val = strings.TrimPrefix(arg, "--auth=")
			i++
			consumed = true
		}

		if consumed {
			parts := strings.SplitN(val, ":", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				log.Fatal("Invalid -auth format. Use: -auth username:password")
			}
			selfCfg.authUser = parts[0]
			selfCfg.authPass = parts[1]
		} else {
			newArgs = append(newArgs, arg)
			i++
		}
	}
	os.Args = newArgs

	// Communicate to migration: create a real user account on first run
	os.Setenv("BESZEL_SELF_MODE", "true")
	if selfCfg.authUser != "" {
		// -auth provided: create account with given credentials
		os.Setenv("USER_EMAIL", selfCfg.authUser+"@beszel.local")
		os.Setenv("USER_PASSWORD", selfCfg.authPass)
	} else {
		// No auth: create account with stable internal password
		// The frontend will auto-login using the /api/beszel/self-autologin endpoint
		hostname, _ := os.Hostname()
		internalPass := "beszel-self-" + hostname + "-monitor"
		os.Setenv("USER_EMAIL", "admin@beszel.local")
		os.Setenv("USER_PASSWORD", internalPass)
		os.Setenv("BESZEL_SELF_INTERNAL_PASS", internalPass)
	}
}

// ─────────────────────────────────────────────
// HUB mode
// ─────────────────────────────────────────────

func runHub() {
	if len(os.Args) > 3 && os.Args[1] == "health" {
		if err := checkHealth(os.Args[3]); err != nil {
			log.Fatal(err)
		}
		fmt.Print("ok")
		return
	}
	h := hub.NewHub(newPocketBase())
	if err := h.StartHub(); err != nil {
		log.Fatal(err)
	}
}

func newPocketBase() *pocketbase.PocketBase {
	isDev := os.Getenv("ENV") == "dev"
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir: beszel.AppName + "_data",
		DefaultDev:     isDev,
	})
	app.RootCmd.Version = beszel.Version
	app.RootCmd.Use = beszel.AppName
	app.RootCmd.Short = ""

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Update " + beszel.AppName + " to the latest version",
		Run:   hub.Update,
	}
	updateCmd.Flags().Bool("china-mirrors", false, "Use mirror (gh.beszel.dev) instead of GitHub")
	app.RootCmd.AddCommand(updateCmd)
	app.RootCmd.AddCommand(newHealthCmd())

	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: isDev,
		Dir:         "../../migrations",
	})
	return app
}

func newHealthCmd() *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check health of running hub",
		Run: func(cmd *cobra.Command, args []string) {
			if err := checkHealth(baseURL); err != nil {
				log.Fatal(err)
			}
			os.Exit(0)
		},
	}
	cmd.Flags().StringVar(&baseURL, "url", "", "base URL")
	cmd.MarkFlagRequired("url")
	return cmd
}

func checkHealth(baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

// ─────────────────────────────────────────────
// AGENT mode
// ─────────────────────────────────────────────

func runAgent() {
	subcommand := ""
	if len(os.Args) > 1 {
		subcommand = os.Args[1]
	}

	switch subcommand {
	case "health":
		if err := health.Check(); err != nil {
			log.Fatal(err)
		}
		fmt.Print("ok")
		return
	case "fingerprint":
		handleAgentFingerprint()
		return
	}

	var key, listen, hubURL, token string
	fs := pflag.NewFlagSet("agent", pflag.ContinueOnError)
	fs.StringVarP(&key, "key", "k", "", "Public key(s) for SSH authentication")
	fs.StringVarP(&listen, "listen", "l", "", "Address or port to listen on")
	fs.StringVarP(&hubURL, "url", "u", "", "URL of the Beszel hub")
	fs.StringVarP(&token, "token", "t", "", "Token for authentication")
	chinaMirrors := fs.BoolP("china-mirrors", "c", false, "Use mirror for updates")
	version := fs.BoolP("version", "v", false, "Show version")
	help := fs.BoolP("help", "h", false, "Show help")

	// backward compat: single-dash long flags
	for i, arg := range os.Args {
		for _, flag := range []string{"key", "listen", "url", "token"} {
			if arg == "-"+flag {
				os.Args[i] = "--" + flag
			} else if strings.HasPrefix(arg, "-"+flag+"=") {
				os.Args[i] = "--" + flag + arg[len("-"+flag):]
			}
		}
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	switch {
	case *version:
		fmt.Println(beszel.AppName+"-agent", beszel.Version)
		return
	case *help || subcommand == "help":
		fmt.Printf("Usage: %s -agent [flags]\n\nFlags:\n", os.Args[0])
		fs.PrintDefaults()
		return
	case subcommand == "update":
		agent.Update(*chinaMirrors)
		return
	}

	if hubURL != "" {
		os.Setenv("HUB_URL", hubURL)
	}
	if token != "" {
		os.Setenv("TOKEN", token)
	}

	keys, err := loadAgentPublicKeys(key)
	if err != nil {
		log.Fatal("Failed to load public keys:", err)
	}

	addr := agent.GetAddress(listen)
	a, err := agent.NewAgent()
	if err != nil {
		log.Fatal("Failed to create agent:", err)
	}
	if err := a.Start(agent.ServerOptions{
		Addr:    addr,
		Network: agent.GetNetwork(addr),
		Keys:    keys,
	}); err != nil {
		log.Fatal("Failed to start agent:", err)
	}
}

func loadAgentPublicKeys(keyFlag string) ([]gossh.PublicKey, error) {
	if keyFlag != "" {
		return agent.ParseKeys(keyFlag)
	}
	if key, ok := utils.GetEnv("KEY"); ok && key != "" {
		return agent.ParseKeys(key)
	}
	keyFile, ok := utils.GetEnv("KEY_FILE")
	if !ok {
		return nil, fmt.Errorf("no key: use -key flag, KEY or KEY_FILE env var")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return agent.ParseKeys(string(data))
}

func handleAgentFingerprint() {
	subCmd := ""
	if len(os.Args) > 2 {
		subCmd = os.Args[2]
	}
	switch subCmd {
	case "", "view":
		dataDir, _ := agent.GetDataDir()
		fmt.Println(agent.GetFingerprint(dataDir, "", ""))
	case "reset":
		dataDir, err := agent.GetDataDir()
		if err != nil {
			log.Fatal(err)
		}
		if err := agent.DeleteFingerprint(dataDir); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Fingerprint reset.")
	default:
		log.Fatalf("Unknown fingerprint subcommand: %q", subCmd)
	}
}

// ─────────────────────────────────────────────
// SELF mode
// ─────────────────────────────────────────────

func runSelf() {
	authDesc := "no login required"
	if selfCfg.authUser != "" {
		authDesc = "login required, user: " + selfCfg.authUser
	}
	fmt.Printf("Beszel %s - self-monitoring mode (%s)\n", beszel.Version, authDesc)

	// Start local agent over unix socket and get its public key
	pubKey, err := startLocalAgent(selfCfg.socketPath)
	if err != nil {
		log.Fatal("Failed to start local agent:", err)
	}

	baseApp := newPocketBase()

	// After hub is fully started, auto-register local system and ensure user
	baseApp.OnServe().BindFunc(func(e *core.ServeEvent) error {
		go func() {
			time.Sleep(800 * time.Millisecond)
			if err := ensureLocalSystem(e.App, pubKey); err != nil {
				log.Println("[self] warning: register local system:", err)
			}
			if err := ensureSelfUser(e.App); err != nil {
				log.Println("[self] warning: ensure user:", err)
			}
		}()
		return e.Next()
	})

	// Register self-mode API endpoints
	baseApp.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// Frontend queries this to know it's in self-mode
		e.Router.GET("/api/beszel/self-info", func(re *core.RequestEvent) error {
			return re.JSON(http.StatusOK, map[string]interface{}{
				"selfMode": true,
				"hasAuth":  selfCfg.authUser != "",
			})
		})

		// Unified self-autologin endpoint:
		//  - no-auth mode:   POST with empty body → auto-login
		//  - -auth mode:     POST with {"password":"..."} → validate password
		e.Router.POST("/api/beszel/self-autologin", func(re *core.RequestEvent) error {
			var email, password string

			if selfCfg.authUser != "" {
				// -auth mode: expect password in request body
				var body struct {
					Password string `json:"password"`
				}
				if err := re.BindBody(&body); err != nil || body.Password == "" {
					return re.JSON(http.StatusBadRequest, map[string]string{
						"error": "password required",
					})
				}
				if body.Password != selfCfg.authPass {
					return re.JSON(http.StatusUnauthorized, map[string]string{
						"error": "wrong password",
					})
				}
				email = selfCfg.authUser + "@beszel.local"
				password = selfCfg.authPass
			} else {
				// No-auth mode: use internal account
				email = "admin@beszel.local"
				internalPass := os.Getenv("BESZEL_SELF_INTERNAL_PASS")
				if internalPass == "" {
					hostname, _ := os.Hostname()
					internalPass = "beszel-self-" + hostname + "-monitor"
				}
				password = internalPass
			}

			record, err := re.App.FindAuthRecordByEmail("users", email)
			if err != nil {
				return re.JSON(http.StatusInternalServerError, map[string]string{
					"error": "user not ready yet, please refresh in a moment",
				})
			}
			if !record.ValidatePassword(password) {
				return re.JSON(http.StatusUnauthorized, map[string]string{
					"error": "authentication failed",
				})
			}

			token, err := record.NewAuthToken()
			if err != nil {
				return re.JSON(http.StatusInternalServerError, map[string]string{
					"error": err.Error(),
				})
			}

			return re.JSON(http.StatusOK, map[string]interface{}{
				"token":  token,
				"record": record,
			})
		})

		return e.Next()
	})

	h := hub.NewHub(baseApp)
	if err := h.StartHub(); err != nil {
		log.Fatal(err)
	}
}

// startLocalAgent starts an agent on a unix socket, returns the public key
func startLocalAgent(socketPath string) (string, error) {
	_, privKeyRaw, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}

	signer, err := gossh.NewSignerFromSigner(privKeyRaw)
	if err != nil {
		return "", fmt.Errorf("create signer: %w", err)
	}
	pubKeyStr := strings.TrimSuffix(string(gossh.MarshalAuthorizedKey(signer.PublicKey())), "\n")

	// Persist private key (hub needs it to connect)
	privKeyPem, err := gossh.MarshalPrivateKey(privKeyRaw, "")
	if err != nil {
		return "", fmt.Errorf("marshal private key: %w", err)
	}
	dataDir := beszel.AppName + "_data"
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", err
	}
	privKeyPath := dataDir + "/self_agent.key"
	if err := os.WriteFile(privKeyPath, pem.EncodeToMemory(privKeyPem), 0600); err != nil {
		return "", fmt.Errorf("write private key: %w", err)
	}

	keys, err := agent.ParseKeys(pubKeyStr)
	if err != nil {
		return "", fmt.Errorf("parse keys: %w", err)
	}

	a, err := agent.NewAgent()
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}

	go func() {
		if err := a.Start(agent.ServerOptions{
			Addr:    socketPath,
			Network: "unix",
			Keys:    keys,
		}); err != nil {
			log.Println("[self] agent stopped:", err)
		}
	}()

	// Wait up to 2 seconds for socket
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return pubKeyStr, nil
}

// ensureLocalSystem creates the "This Server" entry in PocketBase if not already present
func ensureLocalSystem(app core.App, pubKey string) error {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "localhost"
	}

	// Already registered?
	systems, _ := app.FindAllRecords("systems")
	for _, sys := range systems {
		if sys.GetString("host") == selfCfg.socketPath {
			return nil
		}
	}

	users, err := app.FindAllRecords("users")
	if err != nil || len(users) == 0 {
		return fmt.Errorf("no users found yet")
	}

	col, err := app.FindCollectionByNameOrId("systems")
	if err != nil {
		return err
	}

	rec := core.NewRecord(col)
	rec.Set("name", hostname)
	rec.Set("host", selfCfg.socketPath)
	rec.Set("port", 0)
	rec.Set("status", "pending")
	rec.Set("users", []string{users[0].Id})

	return app.Save(rec)
}

// ensureSelfUser makes sure the correct user account exists and has the right password
func ensureSelfUser(app core.App) error {
	var email, password string
	if selfCfg.authUser != "" {
		email = selfCfg.authUser + "@beszel.local"
		password = selfCfg.authPass
	} else {
		email = "admin@beszel.local"
		hostname, _ := os.Hostname()
		password = "beszel-self-" + hostname + "-monitor"
	}

	user, err := app.FindAuthRecordByEmail("users", email)
	if err != nil {
		// Not found – create it
		col, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		user = core.NewRecord(col)
		user.SetEmail(email)
		user.Set("role", "admin")
		user.SetVerified(true)
	}

	// Always set/reset password (handles credential changes between runs)
	user.SetPassword(password)
	return app.Save(user)
}
