package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const usage = "usage: lineargsrun [-args_file FILE] -output FILE -- TOOL [ARG...]"

type config struct {
	argsFile string
	output   string
	tool     string
	toolArgs []string
}

func parseArgs(args []string) (config, error) {
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return config{}, errors.New("missing -- separator before TOOL")
	}
	if separator+1 >= len(args) {
		return config{}, errors.New("missing TOOL after -- separator")
	}

	var cfg config
	flags := flag.NewFlagSet("lineargsrun", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.argsFile, "args_file", "", "line-oriented arguments file")
	flags.StringVar(&cfg.output, "output", "", "output that TOOL must create")
	if err := flags.Parse(args[:separator]); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected argument before -- separator: %q", flags.Arg(0))
	}
	if cfg.output == "" {
		return config{}, errors.New("-output is required")
	}
	cfg.tool = args[separator+1]
	cfg.toolArgs = append([]string(nil), args[separator+2:]...)
	return cfg, nil
}

func readLineArgs(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var args []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words, err := shellWords(line)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		args = append(args, words...)
	}
	return args, scanner.Err()
}

// shellWords splits line as sh splits a command line, removing single quotes,
// double quotes and backslash escapes. Kbuild substitutes the bindgen
// parameters into a shell recipe, so a quoted value such as
// lru_status='#[cfi_encoding="10lru_status"]' reaches bindgen unquoted.
func shellWords(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			word.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) && strings.IndexByte("$`\"\\", line[i+1]) >= 0 {
					i++
				}
				word.WriteByte(line[i])
			}
			if i >= len(line) {
				return nil, errors.New("unterminated double quote")
			}
			inWord = true
		case c == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, nil
}

// readResponseFile reads a compiler response file that holds one argument per
// line, as kernelflags writes them.
func readResponseFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			args = append(args, line)
		}
	}
	return args, nil
}

// expandedArgs substitutes the placeholders in template and expands @FILE
// response files, which libclang-based tools such as bindgen do not read.
func expandedArgs(template []string, argsFile string, fileArgs []string, output string) ([]string, error) {
	var out []string
	foundFile := false
	for _, arg := range template {
		switch {
		case arg == "{args_file}":
			foundFile = true
			out = append(out, fileArgs...)
		case arg == "{output}":
			out = append(out, output)
		case strings.HasPrefix(arg, "@") && len(arg) > 1:
			response, err := readResponseFile(arg[1:])
			if err != nil {
				return nil, fmt.Errorf("read response file: %w", err)
			}
			out = append(out, response...)
		default:
			out = append(out, arg)
		}
	}
	if argsFile != "" && !foundFile {
		return nil, errors.New("TOOL arguments are missing {args_file}")
	}
	if argsFile == "" && foundFile {
		return nil, errors.New("TOOL arguments use {args_file} without -args_file")
	}
	return out, nil
}

func run(cfg config, stdout, stderr io.Writer) error {
	var fileArgs []string
	if cfg.argsFile != "" {
		var err error
		fileArgs, err = readLineArgs(cfg.argsFile)
		if err != nil {
			return err
		}
	}
	args, err := expandedArgs(cfg.toolArgs, cfg.argsFile, fileArgs, cfg.output)
	if err != nil {
		return err
	}
	cmd := exec.Command(cfg.tool, args...)
	cmd.Env = []string{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.output); err != nil {
		return fmt.Errorf("tool did not create output %q: %w", cfg.output, err)
	}
	return nil
}

func main() {
	cfg, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "lineargsrun: %v\n%s\n", err, usage)
		os.Exit(2)
	}
	if err := run(cfg, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "lineargsrun: %v\n", err)
		os.Exit(1)
	}
}
