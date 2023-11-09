package main

import (
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)
import "github.com/hashicorp/go-version"

type pkgpath = string
type semver = *version.Version

func docDir(pkg pkgpath, v semver) string {
	return pkg + "/" + v.String()
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

	cmd_add := exec.Command("futhark", "pkg", "add", pkg, v.String())
	cmd_add.Dir = tmpdir
	cmd_add.Stderr = os.Stderr
	if err := cmd_add.Run(); err != nil {
		return fmt.Errorf("futhark pkg add %s %s: %v", pkg, v, err)
	}

	cmd_sync := exec.Command("futhark", "pkg", "sync")
	cmd_sync.Dir = tmpdir
	cmd_sync.Stderr = os.Stderr
	if err := cmd_sync.Run(); err != nil {
		return fmt.Errorf("futhark pkg sync: %v", err)
	}

	cmd_doc := exec.Command("futhark" , "doc", "lib/"+pkg, "-o", outdir_abs)
	cmd_doc.Dir = tmpdir
	cmd_doc.Stderr = os.Stderr
	if err := cmd_doc.Run(); err != nil {
		return fmt.Errorf("futhark doc %s -o %s: %v", "lib/"+pkg, outdir_abs, err)
	}

	return nil
}

type Pkg struct {
	Path pkgpath
	Desc string
}

func readPkgPaths(f string) (ret []Pkg, err error) {
	pkgsFile, err := os.Open(f)
	if err != nil {
		return nil, err
	}

	defer pkgsFile.Close()
	scanner := bufio.NewScanner(pkgsFile)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		var pkg = scanner.Text()
		if len(pkg) == 0 || pkg[0] == '#' {
			continue
		}
		parts := strings.SplitN(pkg, " ", 2)
		if len(parts) == 2 {
			ret = append(ret, Pkg{parts[0], parts[1]})
		} else {
			ret = append(ret, Pkg{parts[0], ""})
		}
	}

	return ret, nil
}

var versionTagRegex = regexp.MustCompile("/v([0-9]+.[0-9]+.[0-9]+)$")

func versionTags(tags []string) (ret []semver) {
	for _, tag := range tags {
		m := versionTagRegex.FindStringSubmatch(tag)
		if m != nil {
			v,_ := version.NewSemver(m[1])
			if v != nil {
				ret = append(ret, v)
			}
		}
	}
	return ret
}

func redirectForLatest(pkg pkgpath, v semver) (err error) {
	latest_d := "pkgs/" + pkg + "/latest"
	_ = os.Mkdir(latest_d, os.ModePerm)

	html_out, err := os.Create(latest_d + "/index.html")
	if err != nil {
		return err
	}
	defer html_out.Close()

	html_writer := bufio.NewWriter(html_out)
	templateInfo := struct {
		Url string
	}{
		"../" + v.String(),
	}
	if err = templates.ExecuteTemplate(html_writer, "redirect.html", templateInfo); err != nil {
		return err
	}
	html_writer.Flush()

	// Also create an SVG file listing the most recently documented version.
	svg_url := "https://img.shields.io/badge/docs-v" + v.String() + "-%235f021f.svg"

	svg_out, err := os.Create("pkgs/" + pkg + "/status.svg")
	if err != nil {
		return err
	}
	defer svg_out.Close()

	resp, err := http.Get(svg_url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(svg_out, resp.Body)

	return err
}

func processPkg(pkg pkgpath, vs []semver) (ret []semver, err error) {
	fmt.Printf("Handling %s...\n", pkg)

	for _, v := range vs {
		d := docDir(pkg, v)
		pkgs_d := "pkgs/" + d
		success := true
		if _, err := os.Stat(pkgs_d); os.IsNotExist(err) {
			fmt.Printf("Building %s.\n", pkgs_d)
			err = mkDocForPkg(pkg, v, pkgs_d)

			if err != nil {
				fmt.Printf("Failed: %v\n", err)
				success = false
			}

			// We create the directory anyway so later
			// invocations of futhark-docbot will not look
			// at this package again.
			err = os.MkdirAll(pkgs_d, os.ModePerm)
			if err != nil {
				fmt.Printf("Failed to create directory: %v\n", err)
			}
		} else {
			fmt.Printf("Skipping %s - already exists.\n", d)
		}
		if (success) {
			ret = append(ret, v)
		}
	}

	sort.Sort(sort.Reverse(version.Collection(ret)))

	// Construct a redirect to the latest version, if it exists.
	if len(ret) > 0 {
		if err = redirectForLatest(pkg, ret[0]); err != nil {
			return nil, err
		}
	}

	return ret, nil
}

func pkgVersions(pkg pkgpath) ([]semver, error) {
	pkg_url := "https://" + pkg
	cmd := exec.Command("git", "ls-remote", "--tags", pkg_url)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote --tags %s: %v", pkg_url, err)
	}

	return versionTags(strings.Split(out.String(), "\n")), err
}

func purgeStatusImage(pkg pkgpath) error {
	// First, let's determine whether the package's README file even
	// contains a status image.
	readme_url := fmt.Sprintf("http://%s/blob/master/README.md", pkg)
	// Specifically, it must contain this string:
	status_url := fmt.Sprintf("https://futhark-lang.org/pkgs/%s/status.svg", pkg)

	resp, err := http.Get(readme_url)
	if err != nil {
		return err
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		return err
	}
	body := buf.String()

	if !strings.Contains(body, status_url) {
		return nil // No need to continue, but not an error.
	}

	re, err := regexp.Compile(fmt.Sprintf("src=\\\\\"(https://camo[^\\\\]+)\\\\\"[^>]* data-canonical-src=\\\\\"%s\\\\\"[^>]*>", status_url))
	if err != nil {
		return err
	}

	tmp := re.FindStringSubmatch(body)
	if tmp == nil {
		return fmt.Errorf("%s: Could not find URL for status image", pkg)
	}
	if tmp[1] == "" {
		return fmt.Errorf("Somehow the URL was now empty?")
	}
	url := tmp[1]

	fmt.Printf("Purging status image %s...\n", url)
	req, err := http.NewRequest("PURGE", url, nil)
	if err != nil {
		return err
	}

	if _, err := http.DefaultClient.Do(req); err != nil {
		return err
	}

	return nil
}

type PkgInfo struct {
	Path     pkgpath
	Newest   semver
	Versions []semver
	Desc     string
}

func processPkgs(pkgs []Pkg) (ret []PkgInfo, err error) {
	for _, pkg := range pkgs {
		vs, err := pkgVersions(pkg.Path)
		if err != nil {
			return nil, err
		}

		m, err := processPkg(pkg.Path, vs)
		if err != nil {
			return nil, err
		}
		if err := purgeStatusImage(pkg.Path); err != nil {
			// Failing to purge status images should not be fatal
			fmt.Println(err)
		}
		// Ignore packages with no working versions.
		if len(m) > 0 {
			ret = append(ret, PkgInfo{
				pkg.Path,
				m[0],
				m,
				pkg.Desc,
			})
		}
	}

	return ret, err
}

var templates = template.Must(template.ParseFiles("index.html", "redirect.html"))

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
		Pkgs []PkgInfo
		Date string
	}{
		pkgdocs,
		time.Now().Format("2 Jan 2006 15:04:05 MST"),
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
