package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const releaseRepository = "L-use/Linux-file-manager"
const updateModeFile = "release-update-mode"
const installationMarker = "filemanager.installed-at"
const installationSeenFile = "installation-first-observed"
const updateBinaryLimit = 64 << 20
const updateBackupName = uploadPrefix + "update-previous"

var buildVersion = "dev"
var releaseTag = regexp.MustCompile(`^v([0-9]{1,9})\.([0-9]{1,9})\.([0-9]{1,9})$`)

type releaseUpdate struct {
	Current           string `json:"current"`
	Latest            string `json:"latest,omitempty"`
	Mode              string `json:"mode"`
	Available         bool   `json:"available"`
	Supported         bool   `json:"supported"`
	Platform          string `json:"platform"`
	InstalledAt       string `json:"installedAt,omitempty"`
	InstallationExact bool   `json:"installationExact"`
}

type githubRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"assets"`
}

type releaseVersions struct {
	Versions []string `json:"versions"`
	NextPage int      `json:"nextPage,omitempty"`
}

func (r githubRelease) installable() bool {
	if r.Draft || r.Prerelease || !releaseTag.MatchString(r.Tag) {
		return false
	}
	var archive, checksum bool
	for _, asset := range r.Assets {
		if asset.State == "uploaded" {
			archive = archive || asset.Name == "file-manager-"+runtime.GOOS+"-"+runtime.GOARCH+".tar.gz"
			checksum = checksum || asset.Name == "SHA256SUMS"
		}
	}
	return archive && checksum
}

type releaseUpdater struct {
	client            *http.Client
	executable        string
	state             string
	ready             chan string
	changed           chan struct{}
	mu                sync.Mutex
	mode              string
	latest            string
	checked           time.Time
	installing        bool
	installedAt       string
	installationExact bool
}

func newReleaseUpdater(executable, state string) *releaseUpdater {
	u := &releaseUpdater{executable: executable, state: state, mode: "manual", ready: make(chan string, 1), changed: make(chan struct{}, 1)}
	u.client = &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		if len(via) >= 6 || req.URL.Scheme != "https" || (host != "github.com" && host != "api.github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
			return errors.New("unexpected GitHub release redirect")
		}
		return nil
	}}
	if data, err := os.ReadFile(filepath.Join(state, updateModeFile)); err == nil {
		mode := strings.TrimSpace(string(data))
		if validUpdateMode(mode) {
			u.mode = mode
		}
	}
	u.installedAt, u.installationExact = installationDate(filepath.Dir(executable), state, time.Now)
	return u
}

func installationDate(appDir, state string, now func() time.Time) (string, bool) {
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		date, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
		if err != nil || date.IsZero() {
			return ""
		}
		return date.UTC().Format(time.RFC3339)
	}
	if date := read(filepath.Join(appDir, installationMarker)); date != "" {
		return date, true
	}
	record := filepath.Join(state, installationSeenFile)
	if date := read(record); date != "" {
		return date, false
	}
	date := now().UTC().Format(time.RFC3339)
	if err := writePrivateSetting(record, []byte(date+"\n"), 0600); err != nil {
		log.Printf("Record installation date: %v", err)
		return "", false
	}
	return date, false
}

func validUpdateMode(mode string) bool {
	return mode == "manual" || mode == "remind" || mode == "auto"
}

func (u *releaseUpdater) setMode(mode string) error {
	if !validUpdateMode(mode) {
		return failure(400, "badRequest")
	}
	if mode == "auto" && !supportedUpdate() {
		return failure(409, "updateUnsupported")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.installing {
		return failure(409, "busy")
	}
	if err := writePrivateSetting(filepath.Join(u.state, updateModeFile), []byte(mode+"\n"), 0600); err != nil {
		return err
	}
	u.mode = mode
	select {
	case u.changed <- struct{}{}:
	default:
	}
	return nil
}

func supportedUpdate() bool {
	return (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") && releaseTag.MatchString(buildVersion)
}

func newerRelease(latest, current string) bool {
	a, b := releaseTag.FindStringSubmatch(latest), releaseTag.FindStringSubmatch(current)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i < 4; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	return false
}

func (u *releaseUpdater) status() releaseUpdate {
	u.mu.Lock()
	defer u.mu.Unlock()
	return releaseUpdate{
		Current: buildVersion, Latest: u.latest, Mode: u.mode,
		Available: newerRelease(u.latest, buildVersion), Supported: supportedUpdate(),
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		InstalledAt: u.installedAt, InstallationExact: u.installationExact,
	}
}

func (u *releaseUpdater) request(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	return resp, nil
}

func (u *releaseUpdater) check(ctx context.Context) (releaseUpdate, error) {
	u.mu.Lock()
	recent := time.Since(u.checked) < time.Minute
	u.mu.Unlock()
	if recent {
		return u.status(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, err := u.request(ctx, "https://api.github.com/repos/"+releaseRepository+"/releases/latest")
	if err != nil {
		log.Printf("Check release: %v", err)
		return releaseUpdate{}, failure(502, "updateCheckError")
	}
	defer resp.Body.Close()
	var result struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || !releaseTag.MatchString(result.Tag) {
		return releaseUpdate{}, failure(502, "updateCheckError")
	}
	u.mu.Lock()
	u.latest, u.checked = result.Tag, time.Now()
	u.mu.Unlock()
	return u.status(), nil
}

func (u *releaseUpdater) releaseMetadata(ctx context.Context, suffix string, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, err := u.request(ctx, "https://api.github.com/repos/"+releaseRepository+"/releases"+suffix)
	if err != nil {
		log.Printf("Read releases: %v", err)
		return failure(502, "updateCheckError")
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(result); err != nil {
		return failure(502, "updateCheckError")
	}
	return nil
}

func (u *releaseUpdater) versions(ctx context.Context, page int) (releaseVersions, error) {
	const pageSize = 30
	var releases []githubRelease
	if err := u.releaseMetadata(ctx, fmt.Sprintf("?per_page=%d&page=%d", pageSize, page), &releases); err != nil {
		return releaseVersions{}, err
	}
	result := releaseVersions{Versions: make([]string, 0, len(releases))}
	for _, release := range releases {
		if release.installable() {
			result.Versions = append(result.Versions, release.Tag)
		}
	}
	slices.SortFunc(result.Versions, func(a, b string) int {
		if newerRelease(a, b) {
			return -1
		}
		if newerRelease(b, a) {
			return 1
		}
		return 0
	})
	if len(releases) == pageSize {
		result.NextPage = page + 1
	}
	return result, nil
}

func (u *releaseUpdater) poll(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-u.changed:
		}
		if u.status().Mode != "manual" {
			state, err := u.check(ctx)
			if err == nil && state.Mode == "auto" && state.Available && state.Supported {
				if err := u.install(ctx, state.Latest, true); err != nil {
					log.Printf("Automatic update: %v", err)
				}
			}
		}
		timer.Reset(6 * time.Hour)
	}
}

func (u *releaseUpdater) restartService() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.installing {
		return failure(409, "busy")
	}
	u.installing = true
	u.ready <- ""
	return nil
}

func (u *releaseUpdater) install(ctx context.Context, tag string, automatic bool) error {
	if !supportedUpdate() {
		return failure(409, "updateUnsupported")
	}
	if tag == buildVersion || !releaseTag.MatchString(tag) {
		return failure(409, "updateUnavailable")
	}
	latest := ""
	if automatic {
		status, err := u.check(ctx)
		if err != nil {
			return err
		}
		if !status.Available || status.Latest != tag {
			return failure(409, "updateUnavailable")
		}
	} else {
		var release githubRelease
		if err := u.releaseMetadata(ctx, "/tags/"+tag, &release); err != nil {
			return err
		}
		if release.Tag != tag || !release.installable() {
			return failure(409, "updateUnavailable")
		}
		if u.status().Mode == "auto" {
			if status, err := u.check(ctx); err == nil {
				latest = status.Latest
			}
		}
	}
	u.mu.Lock()
	if u.installing {
		u.mu.Unlock()
		return failure(409, "busy")
	}
	u.installing = true
	u.mu.Unlock()
	prepared := false
	defer func() {
		if !prepared {
			u.mu.Lock()
			u.installing = false
			u.mu.Unlock()
		}
	}()

	reportOperation(ctx, "updating", 0, 0)
	path, err := u.download(ctx, tag)
	if err != nil {
		log.Printf("Prepare update %s: %v", tag, err)
		return failure(502, "updateInstallError")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if ctx.Err() != nil || (automatic && u.mode != "auto") {
		os.Remove(path)
		return failure(409, "updateUnavailable")
	}
	if !automatic && u.mode == "auto" && (tag != latest || newerRelease(buildVersion, tag)) {
		if err := writePrivateSetting(filepath.Join(u.state, updateModeFile), []byte("manual\n"), 0600); err != nil {
			os.Remove(path)
			return err
		}
		u.mode = "manual"
	}
	u.ready <- path
	prepared = true
	return nil
}

func (u *releaseUpdater) download(ctx context.Context, tag string) (string, error) {
	base := "https://github.com/" + releaseRepository + "/releases/download/" + tag + "/"
	archiveName := "file-manager-" + runtime.GOOS + "-" + runtime.GOARCH
	resp, err := u.request(ctx, base+"SHA256SUMS")
	if err != nil {
		return "", err
	}
	manifest, err := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	var checksum []byte
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName+".tar.gz" {
			checksum, err = hex.DecodeString(fields[0])
			if err != nil {
				return "", err
			}
			break
		}
	}
	if len(checksum) != sha256.Size {
		return "", errors.New("release checksum missing")
	}

	archive, err := os.CreateTemp(u.state, ".update-archive-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	resp, err = u.request(ctx, base+archiveName+".tar.gz")
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	var downloaded int64
	update := func(n int) {
		downloaded += int64(n)
		reportOperation(ctx, "updateDownloading", downloaded, resp.ContentLength)
	}
	update(0)
	n, err := io.Copy(progressWriter{io.MultiWriter(archive, digest), update}, io.LimitReader(resp.Body, updateBinaryLimit+1))
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	reportOperation(ctx, "updateVerifying", 0, 0)
	if n > updateBinaryLimit || !slices.Equal(checksum, digest.Sum(nil)) {
		return "", errors.New("release checksum mismatch or archive too large")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", err
	}
	defer compressed.Close()
	return stageReleaseBinary(compressed, archiveName+"/filemanager", filepath.Dir(u.executable))
}

func stageReleaseBinary(archive io.Reader, member, directory string) (string, error) {
	reader := tar.NewReader(io.LimitReader(archive, updateBinaryLimit+1))
	for {
		header, err := reader.Next()
		if err != nil {
			return "", err
		}
		if header.Name != member {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > updateBinaryLimit {
			return "", errors.New("invalid executable in release archive")
		}
		out, err := os.CreateTemp(directory, uploadPrefix+"update-")
		if err != nil {
			return "", err
		}
		keep := false
		defer func() {
			out.Close()
			if !keep {
				os.Remove(out.Name())
			}
		}()
		if _, err := io.CopyN(out, reader, header.Size); err != nil {
			return "", err
		}
		if runtime.GOOS == "darwin" {
			image, err := macho.NewFile(out)
			if err != nil {
				return "", err
			}
			if image.Type != macho.TypeExec || (runtime.GOARCH == "arm64" && image.Cpu != macho.CpuArm64) || (runtime.GOARCH == "amd64" && image.Cpu != macho.CpuAmd64) {
				return "", errors.New("release executable does not match the server")
			}
		} else {
			image, err := elf.NewFile(out)
			if err != nil {
				return "", err
			}
			if image.Class != elf.ELFCLASS64 ||
				(runtime.GOARCH == "amd64" && image.Machine != elf.EM_X86_64) ||
				(runtime.GOARCH == "arm64" && image.Machine != elf.EM_AARCH64) ||
				(image.Type != elf.ET_EXEC && image.Type != elf.ET_DYN) {
				return "", errors.New("release executable does not match the server")
			}
		}
		if err := out.Chmod(0755); err != nil {
			return "", err
		}
		if err := out.Sync(); err != nil {
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		keep = true
		return out.Name(), nil
	}
}

type releaseRestart struct {
	staged, executable string
	args               []string
}

func (r *releaseRestart) Error() string { return "restart service" }

func (r *releaseRestart) exec() error {
	defer os.Remove(r.staged)
	args := append([]string{r.executable, "-foreground"}, r.args...)
	_ = os.Unsetenv(daemonEnv)
	if r.staged == "" {
		return syscall.Exec(r.executable, args, os.Environ())
	}
	backup := filepath.Join(filepath.Dir(r.executable), updateBackupName)
	if err := os.Link(r.executable, backup); err != nil {
		log.Printf("Back up executable: %v", err)
		os.Remove(r.staged)
		return syscall.Exec(r.executable, args, os.Environ())
	}
	if err := os.Rename(r.staged, r.executable); err != nil {
		log.Printf("Replace executable: %v", err)
		os.Remove(backup)
		os.Remove(r.staged)
		return syscall.Exec(r.executable, args, os.Environ())
	}
	if err := syscall.Exec(r.executable, args, os.Environ()); err != nil {
		log.Printf("Start replacement executable: %v", err)
		if rollbackErr := os.Rename(backup, r.executable); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
	}
	return syscall.Exec(r.executable, args, os.Environ())
}
