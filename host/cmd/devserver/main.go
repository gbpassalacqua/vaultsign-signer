// Command devserver hosts the test/ directory on http://localhost:8080
// for the dev-mode Chrome extension to attach a content script to.
//
//	go run ./cmd/devserver -dir ../test -addr :8080
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	dir := flag.String("dir", "../test", "directory to serve")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	if _, err := os.Stat(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "directory not accessible:", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/", noCache(http.FileServer(http.Dir(*dir))))

	log.Printf("serving %s on http://localhost%s", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// noCache prevents Chrome from caching index.html / app.js so iterating
// on the test page doesn't require a hard reload.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		h.ServeHTTP(w, r)
	})
}
