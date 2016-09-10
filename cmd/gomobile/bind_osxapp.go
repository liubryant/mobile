// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
  "fmt"
  "go/build"
  "io"
  "io/ioutil"
  "os/exec"
  "path/filepath"
  "strings"
  "text/template"
)

func goOSXBind(pkgs []*build.Package) error {
  typesPkgs, err := loadExportData(pkgs, darwinOsxEnv)
  if err != nil {
    return err
  }

  binder, err := newBinder(typesPkgs)
  if err != nil {
    return err
  }

  name := binder.pkgs[0].Name()
  title := strings.Title(name)

  if buildO != "" && !strings.HasSuffix(buildO, ".framework") {
    return fmt.Errorf("static framework name %q missing .framework suffix", buildO)
  }
  if buildO == "" {
    buildO = title + ".framework"
  }

  srcDir := filepath.Join(tmpdir, "src", "gomobile_bind")
  for _, pkg := range binder.pkgs {
    if err := binder.GenGo(pkg, binder.pkgs, srcDir); err != nil {
      return err
    }
  }
  // Generate the error type.
  if err := binder.GenGo(nil, binder.pkgs, srcDir); err != nil {
    return err
  }
  mainFile := filepath.Join(tmpdir, "src/osxbin/main.go")
  err = writeFile(mainFile, func(w io.Writer) error {
    _, err := w.Write(osxBindFile)
    return err
  })
  if err != nil {
    return fmt.Errorf("failed to create the binding package for OSX: %v", err)
  }

  fileBases := make([]string, len(typesPkgs)+1)
  for i, pkg := range binder.pkgs {
    if fileBases[i], err = binder.GenObjc(pkg, binder.pkgs, srcDir); err != nil {
      return err
    }
  }
  if fileBases[len(fileBases)-1], err = binder.GenObjc(nil, binder.pkgs, srcDir); err != nil {
    return err
  }
  if err := binder.GenObjcSupport(srcDir); err != nil {
    return err
  }
  if err := binder.GenGoSupport(srcDir); err != nil {
    return err
  }

  cmd := exec.Command("xcrun", "lipo", "-create")

  for _, env := range [][]string{darwinOsxEnv} {
    arch := archClang(getenv(env, "GOARCH"))
    path, err := goOSXBindArchive(name, mainFile, env, fileBases)
    if err != nil {
      return fmt.Errorf("darwin-%s: %v", arch, err)
    }
    cmd.Args = append(cmd.Args, "-arch", arch, path)
  }

  // Build static framework output directory.
  if err := removeAll(buildO); err != nil {
    return err
  }
  headers := buildO + "/Versions/A/Headers"
  if err := mkdir(headers); err != nil {
    return err
  }
  if err := symlink("A", buildO+"/Versions/Current"); err != nil {
    return err
  }
  if err := symlink("Versions/Current/Headers", buildO+"/Headers"); err != nil {
    return err
  }
  if err := symlink("Versions/Current/"+title, buildO+"/"+title); err != nil {
    return err
  }

  cmd.Args = append(cmd.Args, "-o", buildO+"/Versions/A/"+title)
  if err := runCmd(cmd); err != nil {
    return err
  }

  // Copy header file next to output archive.
  headerFiles := make([]string, len(fileBases))
  if len(fileBases) == 1 {
    headerFiles[0] = title + ".h"
    err = copyFile(
      headers+"/"+title+".h",
      srcDir+"/"+bindPrefix+title+".h",
    )
    if err != nil {
      return err
    }
  } else {
    for i, fileBase := range fileBases {
      headerFiles[i] = fileBase + ".h"
      err = copyFile(
        headers+"/"+fileBase+".h",
        srcDir+"/"+fileBase+".h")
      if err != nil {
        return err
      }
    }
    headerFiles = append(headerFiles, title+".h")
    err = writeFile(headers+"/"+title+".h", func(w io.Writer) error {
      return osxBindHeaderTmpl.Execute(w, map[string]interface{}{
        "pkgs": pkgs, "title": title, "bases": fileBases,
      })
    })
    if err != nil {
      return err
    }
  }

  resources := buildO + "/Versions/A/Resources"
  if err := mkdir(resources); err != nil {
    return err
  }
  if err := symlink("Versions/Current/Resources", buildO+"/Resources"); err != nil {
    return err
  }
  if err := ioutil.WriteFile(buildO+"/Resources/Info.plist", []byte(osxBindInfoPlist), 0666); err != nil {
    return err
  }

  var mmVals = struct {
    Module  string
    Headers []string
  }{
    Module:  title,
    Headers: headerFiles,
  }
  err = writeFile(buildO+"/Versions/A/Modules/module.modulemap", func(w io.Writer) error {
    return osxModuleMapTmpl.Execute(w, mmVals)
  })
  if err != nil {
    return err
  }
  return symlink("Versions/Current/Modules", buildO+"/Modules")
}

const osxBindInfoPlist = `<?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
    <plist version="1.0">
      <dict>
      </dict>
    </plist>
`

var osxModuleMapTmpl = template.Must(template.New("osxmmap").Parse(`framework module "{{.Module}}" {
{{range .Headers}}    header "{{.}}"
{{end}}
    export *
}`))

func goOSXBindArchive(name, path string, env, fileBases []string) (string, error) {
  arch := getenv(env, "GOARCH")
  archive := filepath.Join(tmpdir, name+"-"+arch+".a")
  err := goBuild(path, env, "-buildmode=c-archive", "-tags=macosx", "-o", archive)
  if err != nil {
    return "", err
  }

  return archive, nil
}

var osxBindFile = []byte(`
package main

import (
  _ "../gomobile_bind"
)

import "C"

func main() {}
`)

var osxBindHeaderTmpl = template.Must(template.New("osx.h").Parse(`
// Objective-C API for talking to the following Go packages
//
{{range .pkgs}}// {{.ImportPath}}
{{end}}//
// File is generated by gomobile bind. Do not edit.
#ifndef __{{.title}}_H__
#define __{{.title}}_H__

{{range .bases}}#include "{{.}}.h"
{{end}}
#endif
`))
