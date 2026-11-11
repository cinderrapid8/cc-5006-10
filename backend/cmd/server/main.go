package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"highlight-assembly/internal/api"
	"highlight-assembly/internal/media"
	"highlight-assembly/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data", "./data", "data directory")
	ffmpeg := flag.String("ffmpeg", envOr("FFMPEG_BIN", "ffmpeg"), "ffmpeg binary path")
	ffprobe := flag.String("ffprobe", envOr("FFPROBE_BIN", "ffprobe"), "ffprobe binary path")
	staticDir := flag.String("static", "", "optional built frontend directory to serve")
	flag.Parse()

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.New(absData)
	if err != nil {
		log.Fatal(err)
	}
	rend := &media.Renderer{FFmpeg: *ffmpeg, FFprobe: *ffprobe}
	srv := api.New(st, rend)

	if *staticDir != "" {
		fs := http.FileServer(http.Dir(*staticDir))
		srv.Mux.Handle("GET /", spaHandler(*staticDir, fs))
	}

	log.Printf("highlight assembly server listening on %s (data=%s)", *addr, absData)
	if err := http.ListenAndServe(*addr, srv.Mux); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// spaHandler serves static assets and falls back to index.html for client routes.
func spaHandler(dir string, fs http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}
}
