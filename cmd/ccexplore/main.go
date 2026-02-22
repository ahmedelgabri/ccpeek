package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/ahmedelgabri/ccexplore/internal/index"
	"github.com/ahmedelgabri/ccexplore/internal/server"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}

	indexOnly := flag.Bool("index-only", false, "Index and exit (don't start server)")
	skipIndex := flag.Bool("skip-index", false, "Skip indexing, serve existing data")
	port := flag.Int("port", 3000, "Server port")
	claudeDir := flag.String("claude-dir", filepath.Join(home, ".claude"), "Source directory")
	dataDir := flag.String("data-dir", filepath.Join(home, ".claude-history"), "Data output/read directory")
	openBrowser := flag.Bool("open", false, "Open browser after starting server")
	flag.Parse()

	if !*skipIndex {
		fmt.Println("Indexing", *claudeDir, "->", *dataDir)
		if err := index.Run(*claudeDir, *dataDir); err != nil {
			log.Fatal("indexing failed: ", err)
		}
		fmt.Println("Indexing complete.")
	}

	if *indexOnly {
		return
	}

	addr := fmt.Sprintf(":%d", *port)
	url := fmt.Sprintf("http://localhost:%d", *port)
	fmt.Println("Serving on", url)

	if *openBrowser {
		openURL(url)
	}

	if err := server.ListenAndServe(addr, *dataDir); err != nil {
		log.Fatal(err)
	}
}

func openURL(url string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	default:
		return
	}
	_ = exec.Command(cmd, url).Start()
}
