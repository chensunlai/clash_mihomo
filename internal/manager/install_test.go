package manager

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerArchitectureChecksumsAndCustomDirectory(t *testing.T) {
	for _, arch := range []string{"x86_64", "aarch64"} {
		t.Run(arch, func(t *testing.T) {
			dir := t.TempDir()
			fakeBin := filepath.Join(dir, "tools")
			if err := os.Mkdir(fakeBin, 0700); err != nil {
				t.Fatal(err)
			}
			binary := []byte("#!/usr/bin/env bash\nprintf 'clashctl v1.0.0\\n'\n")
			payload := filepath.Join(dir, "payload")
			if err := os.WriteFile(payload, binary, 0700); err != nil {
				t.Fatal(err)
			}
			checksum := fmt.Sprintf("%x", sha256.Sum256(binary))
			assetArch := "amd64"
			if arch == "aarch64" {
				assetArch = "arm64"
			}
			uname := "#!/usr/bin/env bash\nif [[ $1 == -s ]]; then echo Linux; else echo " + arch + "; fi\n"
			curl := `#!/usr/bin/env bash
output=""
url=""
while (($#)); do
  if [[ $1 == -o ]]; then output="$2"; shift 2; continue; fi
  if [[ $1 == https://* ]]; then url="$1"; fi
  shift
done
printf '%s\n' "$url" >> "$INSTALL_TEST_LOG"
if [[ $url == */SHA256SUMS ]]; then
  printf '%s  clashctl-linux-%s\n' "$INSTALL_TEST_CHECKSUM" "$INSTALL_TEST_ASSET" > "$output"
else
  cp "$INSTALL_TEST_PAYLOAD" "$output"
fi
`
			if err := os.WriteFile(filepath.Join(fakeBin, "uname"), []byte(uname), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(curl), 0700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(dir, "custom installation")
			log := filepath.Join(dir, "curl.log")
			invoke := func(sum string) ([]byte, error) {
				command := exec.Command("bash", "../../install.sh", "--yes", "--dir", root, "--version", "v1.0.0", "--skip-core", "--no-link")
				command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"), "INSTALL_TEST_LOG="+log, "INSTALL_TEST_CHECKSUM="+sum, "INSTALL_TEST_ASSET="+assetArch, "INSTALL_TEST_PAYLOAD="+payload)
				return command.CombinedOutput()
			}
			output, err := invoke(checksum)
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			installed := filepath.Join(root, "bin", "clashctl")
			data, err := os.ReadFile(installed)
			if err != nil || string(data) != string(binary) {
				t.Fatal("installed wrong payload")
			}
			urls, _ := os.ReadFile(log)
			if !strings.Contains(string(urls), "/v1.0.0/clashctl-linux-"+assetArch) {
				t.Fatal("downloaded wrong architecture")
			}
			output, err = invoke(strings.Repeat("0", 64))
			if err == nil {
				t.Fatal("accepted corrupt checksum")
			}
			after, _ := os.ReadFile(installed)
			if string(after) != string(binary) {
				t.Fatal("checksum failure replaced existing binary")
			}
			matches, _ := filepath.Glob(filepath.Join(root, ".install-*"))
			if len(matches) != 0 {
				t.Fatal("installer left temporary files")
			}
		})
	}
}

func TestInstallerWizardConsentAndBashrcInitialization(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	if err := os.WriteFile(payload, []byte("#!/usr/bin/env bash\necho 'clashctl v1.0.1'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	bashrc := filepath.Join(dir, "bashrc")
	original := "alias keep_me='echo user configuration'\n"
	if err := os.WriteFile(bashrc, []byte(original), 0640); err != nil {
		t.Fatal(err)
	}
	defaultRoot := filepath.Join(dir, "default install")
	selectedRoot := filepath.Join(dir, "custom ' $literal install")
	invoke := func(root, answers string, extra ...string) ([]byte, error) {
		args := []string{"../../install.sh", "--interactive", "--binary", payload, "--dir", root, "--skip-core", "--no-link", "--bashrc", bashrc}
		args = append(args, extra...)
		command := exec.Command("bash", args...)
		command.Stdin = strings.NewReader(answers)
		return command.CombinedOutput()
	}
	output, err := invoke(defaultRoot, selectedRoot+"\nY\ny\nn\n")
	if err != nil {
		t.Fatalf("wizard failed: %v\n%s", err, output)
	}
	if _, err = os.Stat(filepath.Join(selectedRoot, "bin", "clashctl")); err != nil {
		t.Fatal("did not use the selected installation directory")
	}
	if _, err = os.Stat(defaultRoot); !os.IsNotExist(err) {
		t.Fatal("created unused default directory")
	}
	content, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), original) {
		t.Fatal("discarded user bashrc configuration")
	}
	info, _ := os.Stat(bashrc)
	if info.Mode().Perm() != 0640 {
		t.Fatal("changed bashrc permissions")
	}
	command := exec.Command("bash", "-c", `source "$1"; printf '%s' "$PATH"`, "bash", bashrc)
	path, err := command.Output()
	if err != nil || !strings.HasPrefix(string(path), selectedRoot+"/bin:") {
		t.Fatalf("generated PATH is not shell-safe: %q (%v)", path, err)
	}
	output, err = invoke(selectedRoot, "\n\ny\nn\n")
	if err != nil {
		t.Fatalf("repeat install failed: %v\n%s", err, output)
	}
	content, _ = os.ReadFile(bashrc)
	if strings.Count(string(content), "# >>> clashctl initialize >>>") != 1 {
		t.Fatal("duplicated bashrc initialization")
	}
	if strings.Count(string(content), original) != 1 {
		t.Fatal("duplicated or removed user configuration")
	}
}

func TestInstallerCanCancelOrDeclineShellChanges(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload")
	bashrc := filepath.Join(dir, "bashrc")
	if err := os.WriteFile(payload, []byte("#!/usr/bin/env bash\necho 'clashctl v1.0.1'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("# User configuration\n")
	if err := os.WriteFile(bashrc, original, 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(root, answers string) ([]byte, error) {
		command := exec.Command("bash", "../../install.sh", "--interactive", "--binary", payload, "--dir", root, "--skip-core", "--no-link", "--bashrc", bashrc)
		command.Stdin = strings.NewReader(answers)
		return command.CombinedOutput()
	}
	cancelled := filepath.Join(dir, "cancelled")
	output, err := invoke(cancelled, "\nn\n")
	if err != nil {
		t.Fatalf("cancel failed: %v\n%s", err, output)
	}
	if _, err = os.Stat(cancelled); !os.IsNotExist(err) {
		t.Fatal("cancelled installation created files")
	}
	installed := filepath.Join(dir, "installed")
	output, err = invoke(installed, "\n\nn\nn\n")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}
	content, _ := os.ReadFile(bashrc)
	if string(content) != string(original) {
		t.Fatal("modified bashrc without consent")
	}
	command := exec.Command("bash", "../../install.sh", "--yes", "--binary", payload, "--dir", installed, "--skip-core", "--no-link", "--bashrc", bashrc)
	if output, err = command.CombinedOutput(); err != nil {
		t.Fatalf("unattended install failed: %v\n%s", err, output)
	}
	content, _ = os.ReadFile(bashrc)
	if string(content) != string(original) {
		t.Fatal("unattended installation silently initialized bashrc")
	}
	if err = os.WriteFile(bashrc, []byte("# >>> clashctl initialize >>>\n# incomplete user block\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(bashrc)
	command = exec.Command("bash", "../../install.sh", "--yes", "--init-bashrc", "--binary", payload, "--dir", installed, "--skip-core", "--no-link", "--bashrc", bashrc)
	if output, err = command.CombinedOutput(); err == nil {
		t.Fatal("accepted malformed managed bashrc block")
	}
	after, _ := os.ReadFile(bashrc)
	if string(before) != string(after) {
		t.Fatal("overwrote malformed bashrc")
	}
}
