package application

import (
	"runtime"
	"runtime/debug"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// VersionReport returns what this build is and which contract versions it reads.
type VersionReport func() (model.VersionInfo, error)

const (
	modelModulePath = "github.com/TomazVidovic533/stackpuddle"
	develVersion    = "(devel)"
)

// VersionInfoFrom builds the version document from the binary's build information, the same facts
// `go version -m` prints: the main module version, the Go toolchain and the stackpuddle dependency version.
// A missing version reads "(devel)". The engine entry joins once the control plane is linked.
func VersionInfoFrom(bi *debug.BuildInfo) model.VersionInfo {
	cli := model.CLIBuild{Version: develVersion, Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	modelVersion := develVersion
	if bi != nil {
		if bi.Main.Version != "" {
			cli.Version = bi.Main.Version
		}
		if bi.GoVersion != "" {
			cli.Go = bi.GoVersion
		}
		goos, goarch := setting(bi, "GOOS"), setting(bi, "GOARCH")
		if goos != "" && goarch != "" {
			cli.Platform = goos + "/" + goarch
		}
		for _, dep := range bi.Deps {
			if dep.Path == modelModulePath && dep.Version != "" {
				modelVersion = dep.Version
			}
		}
	}
	return model.NewVersionInfo(cli, model.ModelBuild{Version: modelVersion, APIVersions: model.SupportedAPIVersions()})
}

func setting(bi *debug.BuildInfo, key string) string {
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}
