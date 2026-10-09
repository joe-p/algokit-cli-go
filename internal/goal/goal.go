// Package goal maps file arguments for `algokit goal` into the algod container's goal_mount directory, equivalent to
// algokit.core.goal.
package goal

import (
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/conf"
	"github.com/joe-p/algokit-cli-go/internal/engine"
	"github.com/joe-p/algokit-cli-go/internal/log"
)

var logger = log.New("algokit.core.goal")

// VolumeMountPathDocker is the goal_mount path inside the algod container (always a POSIX path).
const VolumeMountPathDocker = "/root/goal_mount/"

// VolumeMountPathLocal returns the local goal_mount directory for the named sandbox.
func VolumeMountPathLocal(directoryName string) string {
	mountPath := filepath.Join(conf.AppConfigDir(), directoryName, "goal_mount")
	if engine.Get() == engine.Podman {
		// Pre create the directory to avoid permission issues
		_ = os.MkdirAll(mountPath, 0o755)
	}
	return mountPath
}

// filenamePattern is `^[\w\-\.]+\.\w+$` with Python's unicode-aware \w.
var filenamePattern = regexp.MustCompile(`^[\p{L}\p{N}\p{Mn}\p{Pc}\-.]+\.[\p{L}\p{N}\p{Mn}\p{Pc}]+$`)

func isSeparator(c byte) bool {
	return c == '/' || (runtime.GOOS == "windows" && c == '\\')
}

// pathParts splits a path into its parts, mirroring pathlib.PurePath.parts (the root is its own part, and "." and
// empty parts are dropped).
func pathParts(argument string) []string {
	var parts []string
	root := filepath.VolumeName(argument)
	rest := argument[len(root):]
	if len(rest) > 0 && isSeparator(rest[0]) {
		root += rest[:1]
		rest = rest[1:]
	}
	if root != "" {
		parts = append(parts, root)
	}
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r < 0x80 && isSeparator(byte(r)) }) {
		if part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}

// IsPathOrFilename reports whether a goal argument looks like a file path (and so needs mapping into the container).
func IsPathOrFilename(argument string) bool {
	parts := pathParts(argument)
	return len(parts) > 1 || (len(parts) == 1 && filenamePattern.MatchString(parts[0]))
}

var outputArgFlags = []string{"-o", "--outdir", "--outfile", "--out", "--result-out", "--lsig-out"}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// PreprocessCommandArgs rewrites any file path arguments to point at the goal_mount directory inside the container,
// copying input files into the local mount directory. Returns the input files, output files and rewritten command.
func PreprocessCommandArgs(command []string, volumeMountPathLocal, dockerMountPath string) (inputFiles, outputFiles, result []string, err error) {
	result = slices.Clone(command)
	for i, arg := range result {
		if !IsPathOrFilename(arg) {
			continue
		}
		absoluteArgPath, absErr := filepath.Abs(conf.ExpandUser(arg))
		if absErr != nil {
			err = absErr
			break
		}
		result[i] = path.Join(dockerMountPath, filepath.Base(absoluteArgPath))

		_, statErr := os.Stat(absoluteArgPath)
		fileExists := statErr == nil
		isOutputArg := i > 0 && slices.Contains(outputArgFlags, result[i-1])
		if fileExists && !isOutputArg {
			inputFiles = append(inputFiles, absoluteArgPath)
			if err = os.MkdirAll(volumeMountPathLocal, 0o755); err != nil {
				break
			}
			if err = copyFile(absoluteArgPath, filepath.Join(volumeMountPathLocal, filepath.Base(absoluteArgPath))); err != nil {
				break
			}
		} else if isOutputArg {
			// it is an output file that doesn't exist yet
			outputFiles = append(outputFiles, absoluteArgPath)
		} else {
			err = &clierr.FileNotFound{Message: arg + " does not exist."}
			break
		}
	}
	if err != nil {
		logger.Error(err.Error())
		return nil, nil, nil, err
	}
	return inputFiles, outputFiles, result, nil
}

func deleteFileFromVolumeMount(filename, volumeMountPath string) {
	if err := os.Remove(filepath.Join(volumeMountPath, filename)); err != nil {
		logger.Error(err.Error())
	}
}

func listFilesInVolume(volumePath string) []string {
	info, err := os.Stat(volumePath)
	if err != nil || !info.IsDir() {
		logger.Error(volumePath + " does not exist or is not a directory.")
		return nil
	}
	var files []string
	_ = filepath.WalkDir(volumePath, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	return files
}

// suffix returns the file extension like pathlib's PurePath.suffix (dotfiles and trailing dots have no suffix).
func suffix(p string) string {
	name := filepath.Base(p)
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// PostProcess cleans up copied input files and copies any output files out of the goal_mount directory.
func PostProcess(inputFiles, outputFiles []string, volumeMountPathLocal string) error {
	for _, inputFile := range inputFiles {
		deleteFileFromVolumeMount(filepath.Base(inputFile), volumeMountPathLocal)
	}

	filesInVolumeMount := listFilesInVolume(volumeMountPathLocal)
	for _, outputFile := range outputFiles {
		ext := suffix(outputFile)
		stem := strings.TrimSuffix(filepath.Base(outputFile), ext)
		// Copy outputs split into multiple files. For example `goal clerk split -i ./input.gtxn -o ./output.txn`
		// will produce a file (output-0.txn etc) for each transaction in the group being split.
		pattern := `^(?:` + regexp.QuoteMeta(stem) + `)(?:-[0-9]+)?`
		if ext != "" {
			pattern += `(?:` + regexp.QuoteMeta(ext) + `)`
		}
		matcher := regexp.MustCompile(pattern + `$`)

		for _, file := range filesInVolumeMount {
			name := filepath.Base(file)
			if !matcher.MatchString(name) {
				continue
			}
			if err := copyFile(filepath.Join(volumeMountPathLocal, name), filepath.Join(filepath.Dir(outputFile), name)); err != nil {
				return err
			}
			deleteFileFromVolumeMount(name, volumeMountPathLocal)
		}
	}
	return nil
}
