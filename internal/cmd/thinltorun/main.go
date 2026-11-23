// Command thinltorun runs the two steps of Kbuild's distributed ThinLTO
// (scripts/Makefile.vmlinux_a and scripts/Makefile.thinlto) as Bazel actions.
//
// The index step runs the linker's thin link with --thinlto-index-only and
// --thinlto-emit-imports-files. It moves each bitcode object's .thinlto.bc
// index to its declared output and copies the objects that the index imports
// from into the object's declared imports directory, at their own paths. An
// object without an index is native; its index and imports stay empty.
//
//	thinltorun index -manifest FILE -- LD [ARG...]
//
// The manifest holds one "object<TAB>index<TAB>imports" line per linker
// input object.
//
// The backend step compiles one object against its index, with the imported
// objects linked back to the paths the index names. An empty index marks a
// native object, which is copied unchanged.
//
//	thinltorun backend -in OBJ -index INDEX -imports DIR -out OUT -- CC [ARG...]
//
// Bazel fixes an action's inputs before it runs, so the imports directory
// gives each backend exactly the objects it reads.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: thinltorun index|backend ..."))
	}
	var err error
	switch os.Args[1] {
	case "index":
		err = runIndex(os.Args[2:])
	case "backend":
		err = runBackend(os.Args[2:])
	default:
		err = fmt.Errorf("unknown thinltorun step %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "thinltorun:", err)
	os.Exit(1)
}

type indexEntry struct {
	object  string
	index   string
	imports string
}

func runIndex(arguments []string) error {
	flags := flag.NewFlagSet("index", flag.ContinueOnError)
	manifest := flags.String("manifest", "", "object and index output pairs")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	command := flags.Args()
	if *manifest == "" || len(command) == 0 {
		return errors.New("index requires -manifest and a linker command")
	}
	entries, err := readManifest(*manifest)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "thinlto-index-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	// The linker writes <prefix><object>.thinlto.bc; an empty old prefix puts
	// every index under the scratch directory.
	linker := append(append([]string{}, command...),
		"--thinlto-index-only="+filepath.Join(scratch, "thinlto-index"),
		"--thinlto-emit-imports-files",
		"--thinlto-prefix-replace=;"+scratch+string(filepath.Separator))
	if err := run(linker); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.MkdirAll(entry.imports, 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(entry.index), 0o755); err != nil {
			return err
		}
		written := filepath.Join(scratch, entry.object+".thinlto.bc")
		if _, err := os.Stat(written); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(entry.index, nil, 0o644); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(written, entry.index); err != nil {
			return err
		}
		if err := copyImports(filepath.Join(scratch, entry.object+".imports"), entry.imports); err != nil {
			return err
		}
	}
	return nil
}

// copyImports copies each object an imports file names into directory, at
// the object's own relative path.
func copyImports(importsFile, directory string) error {
	data, err := os.ReadFile(importsFile)
	if err != nil {
		return err
	}
	for _, module := range strings.Split(string(data), "\n") {
		if module == "" {
			continue
		}
		if !filepath.IsLocal(module) {
			return fmt.Errorf("imported module %q is not a relative path", module)
		}
		destination := filepath.Join(directory, module)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := copyFile(module, destination); err != nil {
			return err
		}
	}
	return nil
}

// linkImports links each object under directory back to its own relative
// path, where the index expects it.
func linkImports(directory string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	return filepath.WalkDir(absolute, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		module, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(module); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(module), 0o755); err != nil {
			return err
		}
		return os.Symlink(path, module)
	})
}

func readManifest(path string) ([]indexEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var entries []indexEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
			return nil, fmt.Errorf("invalid manifest line %q", line)
		}
		entries = append(entries, indexEntry{object: fields[0], index: fields[1], imports: fields[2]})
	}
	return entries, scanner.Err()
}

func runBackend(arguments []string) error {
	flags := flag.NewFlagSet("backend", flag.ContinueOnError)
	input := flags.String("in", "", "bitcode or native object")
	index := flags.String("index", "", "ThinLTO index from the index step")
	imports := flags.String("imports", "", "imported objects from the index step")
	output := flags.String("out", "", "native object")
	nativeOnly := flags.Bool("native_only", false, "the object saved no compiler flags, so it must be native")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	command := flags.Args()
	if *input == "" || *index == "" || *imports == "" || *output == "" || len(command) == 0 {
		return errors.New("backend requires -in, -index, -imports, -out and a compiler command")
	}
	info, err := os.Stat(*index)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return copyFile(*input, *output)
	}
	if *nativeOnly {
		return fmt.Errorf("%s is bitcode but saved no compiler flags for its ThinLTO backend", *input)
	}
	if err := linkImports(*imports); err != nil {
		return err
	}
	// Kbuild's cmd_cc_o_bc.
	compiler := append(append([]string{}, command...),
		"-x", "ir", "-fno-lto", "-Wno-unused-command-line-argument",
		"-fthinlto-index="+*index, "-c", "-o", *output, *input)
	return run(compiler)
}

func run(command []string) error {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", command[0], err)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
