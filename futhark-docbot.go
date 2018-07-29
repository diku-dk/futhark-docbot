package main

import (
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type pkgpath = string
type semver = string

func docDir(pkg pkgpath, v semver) string {
	return pkg + "-" + v
}

func mkDocForPkg(pkg pkgpath, v semver, outdir string) error {
	outdir_abs, err := filepath.Abs(outdir)
	if err != nil {
		return err
	}

	tmpdir, err := ioutil.TempDir("", "docbot")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tmpdir)

	cmd_add := exec.Command("futhark-pkg", "add", pkg, v)
	cmd_add.Dir = tmpdir
	if err := cmd_add.Run(); err != nil {
		return err
	}

	cmd_sync := exec.Command("futhark-pkg", "sync")
	cmd_sync.Dir = tmpdir
	if err := cmd_sync.Run(); err != nil {
		return err
	}

	cmd_doc := exec.Command("futhark-doc", "lib/"+pkg, "-o", outdir_abs)
	cmd_doc.Dir = tmpdir
	if err := cmd_doc.Run(); err != nil {
		return err
	}

	return nil
}

func readPkgPaths(f string) ([]pkgpath, error) {
	pkgsFile, err := os.Open(f)
	if err != nil {
		return nil, err
	}

	defer pkgsFile.Close()
	scanner := bufio.NewScanner(pkgsFile)
	scanner.Split(bufio.ScanLines)

	var pkgs []pkgpath

	for scanner.Scan() {
		var pkg = scanner.Text()
		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}

var versionTagRegex = regexp.MustCompile("/v([0-9]+.[0-9]+.[0-9]+)$")

func versionTags(tags []string) (ret []semver) {
	for _, tag := range tags {
		m := versionTagRegex.FindStringSubmatch(tag)
		if m != nil {
			ret = append(ret, m[1])
		}
	}
	return ret
}

func processPkg(pkg pkgpath, vs []semver) (ret map[semver]string, err error) {
	fmt.Printf("Handling %s...\n", pkg)
	ret = make(map[semver]string)

	for _, v := range vs {
		d := docDir(pkg, v)
		pkgs_d := "pkgs/" + d
		if _, err := os.Stat(pkgs_d); os.IsNotExist(err) {
			fmt.Printf("Building %s.\n", pkgs_d)
			err = mkDocForPkg(pkg, v, pkgs_d)
			if err != nil {
				return nil, err
			}
		} else {
			fmt.Printf("Skipping %s - already exists.\n", d)
		}
		ret[v] = d
	}

	return ret, nil
}

func pkgVersions(pkg pkgpath) ([]semver, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", "https://"+pkg)
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	return versionTags(strings.Split(out.String(), "\n")), err
}

func processPkgs(pkgs []string) (ret map[pkgpath]map[semver]string, err error) {
	ret = make(map[pkgpath]map[semver]string)

	for _, pkg := range pkgs {
		vs, err := pkgVersions(pkg)
		if err != nil {
			return nil, err
		}

		m, err := processPkg(pkg, vs)
		if err != nil {
			return nil, err
		}
		ret[pkg] = m
	}

	return ret, err
}

var templates = template.Must(template.ParseFiles("index.html"))

func processPkgsInFile(f string) (err error) {
	pkgs, err := readPkgPaths(f)
	if err != nil {
		return err
	}

	pkgdocs, err := processPkgs(pkgs)
	if err != nil {
		return err
	}

	html_out, err := os.Create("pkgs/index.html")
	if err != nil {
		return err
	}

	templateInfo := struct {
		Pkgs map[pkgpath]map[semver]string
		Date string
	}{
		pkgdocs,
		time.Now().Format("Jan 2 15:04:05 MST 2006"),
	}

	html_writer := bufio.NewWriter(html_out)
	if err = templates.ExecuteTemplate(html_writer, "index.html", templateInfo); err != nil {
		return err
	}
	html_writer.Flush()

	return nil
}

func main() {
	if err := processPkgsInFile(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}
