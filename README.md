# futhark-docbot

This program uses `futhark-pkg` and `futhark-doc` to automatically
generate an index of known Futhark packages.  Specifically, it reads a
list of package paths from `pkgpaths.txt` and makes the index and
documentation for each package [available on the Futhark
website](https://futhark-lang.org/pkgs).

The index is rebuilt once per day, or whenever there is a commit to
this repository.  If you want to add your Futhark package to
`pkgpaths.txt`, simply issue a full request.
