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
				command := exec.Command("bash", "../../install.sh", "--dir", root, "--version", "v1.0.0", "--skip-core", "--no-link")
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
