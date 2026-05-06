// Command buildext packages the Chrome Web Store version of the extension
// from extension/manifest.store.json + extension/{background.js,
// content-script.js, icons/}.
//
//	go run ./cmd/buildext
//
// The dev manifest at extension/manifest.json (which contains
// http://localhost:8080 and https://*.vercel.app entries that would draw
// rejection in CWS manual review) is intentionally NOT included. We use an
// explicit allowlist of files copied into the dist tree so that secrets
// like extension-key.pem can never accidentally end up in the published zip.
package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// allowlist of files copied into the dist tree, relative to the extension
// source directory. Keep tight — anything not listed never reaches the zip.
var allowedFiles = []string{
	"background.js",
	"content-script.js",
	"icons/16.png",
	"icons/48.png",
	"icons/128.png",
}

func main() {
	extDir := flag.String("ext", "../extension", "extension source directory")
	distDir := flag.String("dist", "../dist", "output directory")
	flag.Parse()

	// Resolve to absolute paths so the messages aren't ambiguous.
	extAbs, err := filepath.Abs(*extDir)
	must(err, "resolve ext")
	distAbs, err := filepath.Abs(*distDir)
	must(err, "resolve dist")

	// Read the Store manifest to (a) put it into the dist tree as
	// manifest.json and (b) pull the version field for the zip filename.
	storeManifestPath := filepath.Join(extAbs, "manifest.store.json")
	manifestBytes, err := os.ReadFile(storeManifestPath)
	must(err, "read manifest.store.json")

	var meta struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	must(json.Unmarshal(manifestBytes, &meta), "parse manifest.store.json")
	if meta.Version == "" {
		fail("manifest.store.json has no version field")
	}

	// Stage directory: dist/extension-store/.
	stageDir := filepath.Join(distAbs, "extension-store")
	must(os.RemoveAll(stageDir), "clean stage dir")
	must(os.MkdirAll(filepath.Join(stageDir, "icons"), 0o755), "mkdir stage")

	// Write the Store manifest as the dist's manifest.json.
	must(os.WriteFile(filepath.Join(stageDir, "manifest.json"), manifestBytes, 0o644), "write manifest.json")

	// Copy allowlisted files only.
	for _, rel := range allowedFiles {
		src := filepath.Join(extAbs, rel)
		dst := filepath.Join(stageDir, rel)
		must(os.MkdirAll(filepath.Dir(dst), 0o755), "mkdir for "+rel)
		must(copyFile(src, dst), "copy "+rel)
	}

	// Zip it.
	zipName := fmt.Sprintf("vaultsign-signer-%s.zip", meta.Version)
	zipPath := filepath.Join(distAbs, zipName)
	must(zipDir(stageDir, zipPath), "zip dist")

	// Report.
	st, err := os.Stat(zipPath)
	must(err, "stat zip")
	fmt.Println("=== buildext ===")
	fmt.Printf("name:    %s\n", meta.Name)
	fmt.Printf("version: %s\n", meta.Version)
	fmt.Printf("stage:   %s\n", stageDir)
	fmt.Printf("zip:     %s\n", zipPath)
	fmt.Printf("size:    %d bytes (%.1f KB)\n", st.Size(), float64(st.Size())/1024)
	fmt.Println()
	fmt.Println("contents:")
	zr, err := zip.OpenReader(zipPath)
	must(err, "reopen zip for listing")
	defer zr.Close()
	for _, f := range zr.File {
		fmt.Printf("  %-30s %d bytes\n", f.Name, f.UncompressedSize64)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func zipDir(srcDir, zipPath string) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		// Forward slashes inside the zip — Chrome and the CWS tools both
		// expect POSIX-style separators.
		rel = filepath.ToSlash(rel)

		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate

		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}

func must(err error, what string) {
	if err != nil {
		fail(what + ": " + err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "buildext: "+msg)
	os.Exit(1)
}
