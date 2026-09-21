package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/javded-itres/open-comfy/internal/auth"
	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/config"
	"github.com/javded-itres/open-comfy/internal/files"
	"github.com/javded-itres/open-comfy/internal/httpapi"
	"github.com/javded-itres/open-comfy/internal/ids"
	"github.com/javded-itres/open-comfy/internal/importwf"
	"github.com/javded-itres/open-comfy/internal/jobs"
)

var version = "0.1.0"

func main() {
	configPath := flag.String("config", os.Getenv("OPENCOMFY_CONFIG"), "path to config.yaml")
	listen := flag.String("listen", "", "override listen address")
	doInit := flag.Bool("init", false, "write example config, keys, hmac secret")
	health := flag.Bool("healthcheck", false, "GET /health and exit 0/1")
	skipWF := flag.Bool("skip-workflow-check", false, "skip workflow validation (CI)")
	doImport := flag.Bool("import-comfy", false, "import selected ComfyUI workflow names (args)")
	importDry := flag.Bool("import-comfy-dry", false, "preview selected names without writing")
	listComfy := flag.Bool("list-comfy", false, "list ComfyUI userdata workflows and mapping errors")
	showVer := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}
	if *doInit {
		key, err := config.InitFiles(*configPath)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote example config; copy configs/ or edit /etc/opencomfy")
		if key != "" {
			fmt.Println("generated API key (shown once):", key)
		}
		return
	}
	if *listComfy || *doImport || *importDry {
		runImport(*configPath, *listComfy, *importDry, flag.Args())
		return
	}
	if *health {
		addr := *listen
		if addr == "" {
			addr = os.Getenv("OPENCOMFY_LISTEN")
		}
		if addr == "" {
			addr = ":8788"
		}
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			port = strings.TrimPrefix(addr, ":")
		}
		resp, err := http.Get("http://127.0.0.1:" + port + "/health")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(config.ExitMissingConfig)
	}
	cfg.Version = version
	cfg.SkipWorkflow = *skipWF
	if *listen != "" {
		cfg.Listen = *listen
	}

	if err := config.EnsureDirs(cfg); err != nil {
		log.Fatal(err)
	}

	secret, err := files.LoadSecret(cfg.Files.HMACSecretFile)
	if err != nil {
		secret, err = files.WriteSecret(cfg.Files.HMACSecretFile)
		if err != nil {
			log.Fatalf("hmac secret: %v", err)
		}
	}
	cfg.Files.HMACSecret = secret

	var extraKeys []string
	if v := os.Getenv("API_KEYS"); v != "" {
		extraKeys = strings.Split(v, ",")
	}
	authSvc, err := auth.Load(cfg.Auth.KeysFile, extraKeys, cfg.Auth.Disabled)
	if err != nil {
		log.Fatal(err)
	}

	cat, err := catalog.Load(cfg.ModelsFile, cfg.WorkflowsDir, cfg.SkipWorkflow)
	if err != nil {
		log.Fatal(err)
	}

	extra := cfg.ComfyUI.ExtraData
	if extra == nil {
		extra = map[string]any{}
	}
	if cfg.ComfyUI.APIKeyEnv != "" {
		if k := os.Getenv(cfg.ComfyUI.APIKeyEnv); k != "" {
			extra["api_key_comfy_org"] = k
		}
	}
	client := comfy.New(
		cfg.ComfyUI.BaseURL,
		cfg.ComfyUI.AuthHeader,
		ids.New("oc-"),
		extra,
		time.Duration(cfg.ComfyUI.RequestTimeoutS)*time.Second,
		time.Duration(cfg.ComfyUI.PollIntervalS)*time.Second,
		cfg.ComfyUI.AllowInterrupt,
	)

	jobStore := jobs.New(cfg.Jobs.Dir, cfg.JobTTL())
	fileStore := files.New(cfg.Files.Dir, secret, cfg.FileTTL(), cfg.Origin)

	api := httpapi.New(cfg, authSvc, cat, client, jobStore, fileStore)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	api.StartWorkers(ctx)

	srv := httpapi.HTTPServer(cfg, api.Handler())
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	cfg.BoundPort = httpapi.ListenerPort(ln)
	log.Printf("opencomfy %s listening on %s origin=%s", version, ln.Addr(), cfg.Origin())

	go func() {
		<-ctx.Done()
		shctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(shctx)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func runImport(configPath string, listOnly, dry bool, names []string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	client := comfy.New(
		cfg.ComfyUI.BaseURL,
		cfg.ComfyUI.AuthHeader,
		ids.New("oc-"),
		cfg.ComfyUI.ExtraData,
		time.Duration(cfg.ComfyUI.RequestTimeoutS)*time.Second,
		time.Duration(cfg.ComfyUI.PollIntervalS)*time.Second,
		false,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if listOnly || len(names) == 0 {
		items, err := importwf.List(ctx, client, cfg.ModelsFile, cfg.WorkflowsDir)
		if err != nil {
			log.Fatal(err)
		}
		for _, it := range items {
			flag := "OK"
			if !it.CanImport {
				flag = "BLOCK"
			}
			fmt.Printf("%s\t%s\t%s\t%v\n", flag, it.Name, it.Modality, it.Errors)
		}
		if !listOnly && len(names) == 0 {
			fmt.Fprintln(os.Stderr, "select workflows: opencomfy -import-comfy -config … \"Name.json\" \"Other.json\"")
			fmt.Fprintln(os.Stderr, "or open http://<host>:8788/import")
			os.Exit(2)
		}
		return
	}
	res, err := importwf.ImportSelected(ctx, client, cfg.ModelsFile, cfg.WorkflowsDir, names, dry)
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range res.Imported {
		fmt.Println("imported:", s)
	}
	for _, s := range res.Skipped {
		fmt.Println("skipped:", s)
	}
	for _, s := range res.Failed {
		fmt.Println("failed:", s)
	}
	if len(res.Failed) > 0 && len(res.Imported) == 0 {
		os.Exit(1)
	}
}
