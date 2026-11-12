package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// entryMode gives archive entries the mode they should have on Unix,
// independent of the filesystem that built the archive: Windows reports
// synthetic 0666 modes, which would make the extracted ai-ssh and
// install-icons.sh non-executable.
func entryMode(rel string, isDir bool) fs.FileMode {
	if isDir {
		return 0o755
	}
	base := filepath.Base(rel)
	if strings.HasSuffix(rel, ".sh") || base == "ai-ssh" || base == "ai-ssh.exe" {
		return 0o755
	}
	return 0o644
}

// writeZip writes the staging directory as a zip whose entries are at the
// root. Unix modes are kept in the entry headers.
func writeZip(stage, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)

	err = filepath.WalkDir(stage, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		header.SetMode(entryMode(rel, d.IsDir()))
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
	if err != nil {
		zw.Close()
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeTarGz writes the staging directory as a gzipped tar whose entries are
// at the root. Modes are preserved so the macOS bundle and install script keep
// their executable bits.
func writeTarGz(stage, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	now := time.Now().UTC()

	err = filepath.WalkDir(stage, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			header.Name += "/"
		}
		header.Mode = int64(entryMode(rel, d.IsDir()))
		header.ModTime = now
		header.AccessTime = now
		header.ChangeTime = now
		// Stable ownership, whoever built the archive.
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	if err != nil {
		tw.Close()
		gz.Close()
		f.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
