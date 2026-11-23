// Command cpufeaturenames writes objtool's cpu-feature-names.c from the tools
// copy of cpufeatures.h, as Linux 7.x's
// tools/arch/x86/tools/gen-cpu-feature-names-x86.awk does.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// featureValue matches the parenthesized word*32+bit index of a feature or
// bug definition.
var featureValue = regexp.MustCompile(`\([0-9*+ ]+\)`)

func main() {
	input := flag.String("in", "", "tools/arch/x86/include/asm/cpufeatures.h")
	output := flag.String("out", "", "cpu-feature-names.c to write")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "-in and -out are required")
		os.Exit(2)
	}
	header, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cpufeaturenames: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, []byte(generate(string(header))), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cpufeaturenames: %v\n", err)
		os.Exit(1)
	}
}

func generate(header string) string {
	var out strings.Builder
	out.WriteString("/* cpu feature name array generated from cpufeatures.h */\n")
	out.WriteString("/* Do not change this code. */\n\n")
	out.WriteString("static const char *cpu_feature_names[(NCAPINTS+NBUGINTS)*32] = {\n")
	for _, line := range strings.Split(header, "\n") {
		feature := strings.HasPrefix(line, "#define X86_FEATURE_")
		bug := strings.HasPrefix(line, "#define X86_BUG_")
		if !feature && !bug {
			continue
		}
		match := featureValue.FindString(line)
		if match == "" {
			continue
		}
		value := match[1 : len(match)-1]
		name := strings.Fields(line)[1]
		if feature {
			fmt.Fprintf(&out, "\t[%s] = \"%s\",\n", value, name)
		} else {
			fmt.Fprintf(&out, "\t[NCAPINTS*32+(%s)] = \"%s\",\n", value, name)
		}
	}
	out.WriteString("};\n")
	return out.String()
}
