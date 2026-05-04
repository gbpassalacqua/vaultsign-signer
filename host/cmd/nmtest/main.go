// Command nmtest simulates Chrome's Native Messaging host lookup and
// launch sequence step-by-step, so we can pinpoint which stage fails
// when Chrome reports "Specified native messaging host not found".
//
//	go run ./cmd/nmtest -name com.vaultsign.signer -extension ijlgpago...
//
// Reproduces what Chrome does:
//   1. Read HKCU\Software\Google\Chrome\NativeMessagingHosts\<name> (default value)
//   2. Read the manifest file at that path
//   3. Parse JSON; verify "name" field matches
//   4. Verify allowed_origins includes the calling extension's chrome-extension://<id>/
//   5. Verify "type":"stdio"
//   6. Spawn the binary at "path" with stdio inherited
//   7. Send {"action":"ping"} as a length-prefixed message
//   8. Read the response
//
// If any step fails, prints which one and the exact OS-level error.

package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

type manifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func main() {
	hostName := flag.String("name", "com.vaultsign.signer", "native host name")
	extID := flag.String("extension", "ijlgpagoomkbimmghbimheocgneioibn", "calling extension ID")
	flag.Parse()

	fmt.Println("=== nmtest: simulating Chrome Native Messaging lookup ===")
	fmt.Println("host name:    ", *hostName)
	fmt.Println("extension ID: ", *extID)
	fmt.Println()

	// Step 1: Open registry — try HKCU first, then HKLM.
	const subKey = `Software\Google\Chrome\NativeMessagingHosts\`
	manifestPath, source, err := readRegistry(*hostName, subKey)
	if err != nil {
		fail("STEP 1: registry lookup", err)
	}
	fmt.Printf("[1] registry [%s]: (default) = %q\n", source, manifestPath)

	// Step 2: Read manifest file.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fail("STEP 2: read manifest file", err)
	}
	fmt.Printf("[2] read manifest file (%d bytes) OK\n", len(data))

	// Step 3: Parse JSON.
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		fail("STEP 3: JSON parse", err)
	}
	fmt.Printf("[3] JSON parse OK — name=%q type=%q path=%q origins=%v\n",
		m.Name, m.Type, m.Path, m.AllowedOrigins)

	// Step 3b: Verify name matches.
	if m.Name != *hostName {
		fail("STEP 3b: manifest name mismatch", fmt.Errorf("manifest name=%q, expected %q", m.Name, *hostName))
	}
	fmt.Printf("[3b] name field matches\n")

	// Step 4: Verify allowed_origins includes the extension.
	want := fmt.Sprintf("chrome-extension://%s/", *extID)
	matched := false
	for _, o := range m.AllowedOrigins {
		if o == want {
			matched = true
			break
		}
	}
	if !matched {
		fail("STEP 4: allowed_origins", fmt.Errorf("origin %q not in %v", want, m.AllowedOrigins))
	}
	fmt.Printf("[4] allowed_origins includes %s\n", want)

	// Step 5: Verify type=stdio.
	if m.Type != "stdio" {
		fail("STEP 5: type", fmt.Errorf("type=%q, expected stdio", m.Type))
	}
	fmt.Printf("[5] type = stdio\n")

	// Step 6: Spawn the binary with stdio piped.
	if _, err := os.Stat(m.Path); err != nil {
		fail("STEP 6a: stat binary", err)
	}
	cmd := exec.Command(m.Path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fail("STEP 6b: stdin pipe", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail("STEP 6c: stdout pipe", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fail("STEP 6d: start binary", err)
	}
	fmt.Printf("[6] spawned %s (pid=%d)\n", m.Path, cmd.Process.Pid)

	// Step 7: Send ping.
	payload := []byte(`{"action":"ping"}`)
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(payload)))
	stdin.Write(hdr[:])
	stdin.Write(payload)
	fmt.Printf("[7] sent ping (%d bytes payload)\n", len(payload))

	// Step 8: Read response with 5s timeout.
	resp := make(chan []byte, 1)
	errs := make(chan error, 1)
	go func() {
		r := bufio.NewReader(stdout)
		var hdrIn [4]byte
		if _, err := io.ReadFull(r, hdrIn[:]); err != nil {
			errs <- fmt.Errorf("read header: %w", err)
			return
		}
		n := binary.LittleEndian.Uint32(hdrIn[:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			errs <- fmt.Errorf("read body: %w", err)
			return
		}
		resp <- buf
	}()
	select {
	case r := <-resp:
		fmt.Printf("[8] received response: %s\n", strings.TrimSpace(string(r)))
		fmt.Println()
		fmt.Println("=== ALL STEPS PASSED ===")
		fmt.Println("If Chrome still says 'host not found', the issue is")
		fmt.Println("specific to Chrome's lookup path — not the manifest, not the exe.")
	case err := <-errs:
		fail("STEP 8: read response", err)
	case <-time.After(5 * time.Second):
		fail("STEP 8: timeout waiting for response", fmt.Errorf("no response in 5s"))
	}

	stdin.Close()
	cmd.Wait()
}

func readRegistry(hostName, subKey string) (manifestPath, source string, err error) {
	for _, hive := range []struct {
		key  registry.Key
		name string
	}{
		{registry.CURRENT_USER, "HKCU"},
		{registry.LOCAL_MACHINE, "HKLM"},
	} {
		k, err := registry.OpenKey(hive.key, subKey+hostName, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		defer k.Close()
		val, _, err := k.GetStringValue("")
		if err != nil {
			continue
		}
		return val, hive.name, nil
	}
	return "", "", fmt.Errorf("registry key for %q not found in HKCU or HKLM", hostName)
}

func fail(stage string, err error) {
	fmt.Println()
	fmt.Printf("FAIL at %s: %v\n", stage, err)
	os.Exit(1)
}
