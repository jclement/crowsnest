package main

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/config"
)

// TestSecretHandoverSplitsTheEnvironment is the planning half of the H1 residual fix: the
// re-exec environment has no secret, and a process that is already the re-executed one does not
// re-exec again.
func TestSecretHandoverSplitsTheEnvironment(t *testing.T) {
	environ := []string{"PATH=/bin", "CROWSNEST_PASSWORD=hunter2", "CROWSNEST_PORT=2769", "ELEVENLABS_API_KEY=sk", "CROWSNEST_NOTIFY_URL=https://ntfy.sh/t"}
	clean, secrets, ok := secretHandover(environ, config.SecretEnvNames())
	if !ok || secrets["CROWSNEST_PASSWORD"] != "hunter2" || secrets["ELEVENLABS_API_KEY"] != "sk" || secrets["CROWSNEST_NOTIFY_URL"] == "" {
		t.Fatalf("secrets %v ok %v", secrets, ok)
	}
	if !slices.Equal(clean, []string{"PATH=/bin", "CROWSNEST_PORT=2769"}) {
		t.Fatalf("re-exec environment %v", clean)
	}
	if _, _, ok := secretHandover(append(environ, secretsFDEnv+"=5"), config.SecretEnvNames()); ok {
		t.Fatal("the re-executed process would re-exec again")
	}
	if _, _, ok := secretHandover([]string{"PATH=/bin"}, config.SecretEnvNames()); ok {
		t.Fatal("re-exec with nothing to hand over")
	}
}

// TestHandedOverSecretsAreRestored is the receiving half: the secrets come back from the pipe
// into the in-memory environment, and only names that are secrets.
func TestHandedOverSecretsAreRestored(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.WriteString(`{"CROWSNEST_PASSWORD":"hunter2","PATH":"/evil"}`)
	_ = writer.Close()
	t.Setenv("CROWSNEST_PASSWORD", "")
	t.Setenv(secretsFDEnv, strconv.Itoa(int(reader.Fd())))
	path := os.Getenv("PATH")
	if restored, err := restoreHandedOverSecrets(); err != nil || !restored {
		t.Fatalf("restored %v, %v", restored, err)
	}
	if restored, _ := restoreHandedOverSecrets(); restored {
		t.Fatal("a second call claimed to be the re-executed process again")
	}
	if os.Getenv("CROWSNEST_PASSWORD") != "hunter2" || os.Getenv("PATH") != path {
		t.Fatalf("restored password %q, PATH %q", os.Getenv("CROWSNEST_PASSWORD"), os.Getenv("PATH"))
	}
	if _, ok := os.LookupEnv(secretsFDEnv); ok {
		t.Fatal("the handover variable stayed in the environment")
	}
}

// TestServeDropsSecretsFromItsOwnEnvironment is the H1 residual end to end: the built binary,
// started with the password in its environment, shows no password in the environment the
// operating system reports for it, and still accepts that password at login.
func TestServeDropsSecretsFromItsOwnEnvironment(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("reads another process's environment the Linux or macOS way")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "cn-reexec-") // short: the demo's socket path must fit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	binary := filepath.Join(dir, "crowsnest")
	if out, err := exec.Command(goTool, "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("building: %v\n%s", err, out)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	_ = listener.Close()
	const secret = "handover-check-7f3a"
	serve := exec.Command(binary, "serve", "--demo", "--web-dev", "--port", port)
	serve.Env = append(scrubbedTestEnv(), "HOME="+dir, "XDG_STATE_HOME="+filepath.Join(dir, "state"),
		"CROWSNEST_DATA_DIR="+filepath.Join(dir, "data"), "CROWSNEST_USERNAME=jeff", "CROWSNEST_PASSWORD="+secret)
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _ = serve.Wait() })

	base := "http://127.0.0.1:" + port
	login := func(password string) int {
		request, _ := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(`{"username":"jeff","password":"`+password+`"}`))
		request.Header.Set("Origin", base)
		request.Header.Set("X-Crowsnest", "1")
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return 0
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	deadline := time.Now().Add(20 * time.Second)
	for login(secret) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("serve never accepted the password it was started with")
		}
		time.Sleep(100 * time.Millisecond)
	}
	environ := processEnvironment(t, serve.Process.Pid)
	if strings.Contains(environ, secret) {
		t.Fatal("the password is still in the environment the OS reports for crowsnest")
	}
	if !strings.Contains(environ, "CROWSNEST_USERNAME=jeff") {
		t.Fatalf("could not read crowsnest's environment, so the check proves nothing: %q", environ)
	}
}

// processEnvironment returns what the OS reports as pid's environment.
func processEnvironment(t *testing.T, pid int) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		body, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(body), "\x00", " ")
	}
	out, err := exec.Command("ps", "eww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// scrubbedTestEnv is this test's environment without HERDR_* and crowsnest secrets.
func scrubbedTestEnv() []string {
	var out []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "HERDR_") || slices.Contains(config.SecretEnvNames(), name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
