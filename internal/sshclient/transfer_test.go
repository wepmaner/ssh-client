package sshclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshclient/internal/sshclient/sshtest"
)

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func writeRandom(t *testing.T, path string, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	src := filepath.Join(localDir, "src.bin")
	data := writeRandom(t, src, 300_000)
	remote := "r.bin" // путь относительно SFTPRoot

	n, err := Upload(context.Background(), c, src, remote, false)
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Upload: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(remoteDir, "r.bin")) //nolint:gosec // G304: test temp path
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("удалённый файл не совпал: %v", err)
	}
	if names := readDirNames(t, remoteDir); len(names) != 1 {
		t.Fatalf("на сервере остались лишние файлы: %v", names)
	}

	dst := filepath.Join(localDir, "dst.bin")
	n, err = Download(context.Background(), c, remote, dst, false)
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Download: n=%d err=%v", n, err)
	}
	got, err = os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("скачанный файл не совпал: %v", err)
	}
	if names := readDirNames(t, localDir); len(names) != 2 {
		t.Fatalf("локально остались лишние файлы: %v", names)
	}
}

func TestUploadRefusesExisting(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	src := filepath.Join(localDir, "src")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(remoteDir, "t")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := "t"
	if _, err := Upload(context.Background(), c, src, remote, false); !errors.Is(err, ErrExists) {
		t.Fatalf("ошибка %v, ожидалась ErrExists", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatalf("цель изменена: %q", b)
	}
	if _, err := Upload(context.Background(), c, src, remote, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("цель не заменена: %q", b)
	}
}

func TestDownloadRefusesExisting(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	remote := filepath.Join(remoteDir, "r")
	if err := os.WriteFile(remote, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(localDir, "d")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), c, "r", dst, false); !errors.Is(err, ErrExists) {
		t.Fatalf("ошибка %v, ожидалась ErrExists", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "old" {
		t.Fatalf("цель изменена: %q", b)
	}
	if _, err := Download(context.Background(), c, "r", dst, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("цель не заменена: %q", b)
	}
}

func TestDownloadInterruptedKeepsTarget(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	srv, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	remote := filepath.Join(remoteDir, "big")
	writeRandom(t, remote, 16<<20)
	dst := filepath.Join(localDir, "d")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	transferHook = func(written int64) {
		if written >= 64<<10 {
			srv.DropConnections()
		}
	}
	t.Cleanup(func() { transferHook = nil })

	if _, err := Download(context.Background(), c, "big", dst, true); err == nil {
		t.Fatal("обрыв не дал ошибки")
	}
	if b, _ := os.ReadFile(dst); string(b) != "old" {
		t.Fatalf("цель изменена: %q", b)
	}
	if names := readDirNames(t, localDir); len(names) != 1 {
		t.Fatalf("остались временные файлы: %v", names)
	}
}

func TestUploadInterruptedRemovesRemoteTemp(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	src := filepath.Join(localDir, "big")
	writeRandom(t, src, 16<<20)
	target := filepath.Join(remoteDir, "t")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transferHook = func(written int64) {
		if written >= 64<<10 {
			cancel()
		}
	}
	t.Cleanup(func() { transferHook = nil })

	_, err := Upload(ctx, c, src, "t", true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка %v, ожидалась context.Canceled", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatalf("цель изменена: %q", b)
	}
	if names := readDirNames(t, remoteDir); len(names) != 1 {
		t.Fatalf("на сервере остался временный файл: %v", names)
	}
	// общий клиент остаётся рабочим
	s, err := c.NewSession()
	if err != nil {
		t.Fatalf("клиент закрыт после Upload: %v", err)
	}
	_ = s.Close()
}

func TestNoSFTPIsClearError(t *testing.T) {
	_, c := execClient(t, sshtest.Opts{}) // SFTPRoot пуст: подсистема отклоняется
	src := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Upload(context.Background(), c, src, "/tmp/x", false)
	if !errors.Is(err, ErrNoSFTP) {
		t.Fatalf("ошибка %v, ожидалась ErrNoSFTP", err)
	}
	if !strings.Contains(ErrNoSFTP.Error(), "SFTP") {
		t.Fatalf("текст ErrNoSFTP: %q", ErrNoSFTP)
	}
}

func TestStalledSFTPHandshakeHonoursContext(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir, StallSFTP: true})
	src := filepath.Join(localDir, "s")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(ctx context.Context) error{
		"upload": func(ctx context.Context) error {
			_, err := Upload(ctx, c, src, "r", true)
			return err
		},
		"download": func(ctx context.Context) error {
			_, err := Download(ctx, c, "r", filepath.Join(localDir, "d"), true)
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		start := time.Now()
		err := run(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
			t.Fatalf("%s: ошибка %v за %v", name, err, time.Since(start))
		}
	}
}

func TestDownloadTargetCreatedDuringTransferSurvives(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	writeRandom(t, filepath.Join(remoteDir, "r"), 1<<20)
	dst := filepath.Join(localDir, "d")
	created := false
	transferHook = func(int64) {
		if !created {
			created = true
			if err := os.WriteFile(dst, []byte("mine"), 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { transferHook = nil })

	if _, err := Download(context.Background(), c, "r", dst, false); !errors.Is(err, ErrExists) {
		t.Fatalf("ошибка %v, ожидалась ErrExists", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "mine" {
		t.Fatalf("цель затёрта: %q", b)
	}
	if names := readDirNames(t, localDir); len(names) != 1 {
		t.Fatalf("остались временные файлы: %v", names)
	}
}
