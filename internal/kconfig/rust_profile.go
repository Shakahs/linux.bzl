package kconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const RustProfileSchema = "linux-rust-profile-v2"

type RustProfile struct {
	Schema             string                  `json:"schema"`
	Architecture       string                  `json:"architecture"`
	SourceLayout       string                  `json:"source_layout"`
	Target             RustTargetRecipe        `json:"target"`
	CommonFlags        RustVersionedFlagSet    `json:"common_flags"`
	TargetFlags        RustFlagSet             `json:"target_flags"`
	Module             RustModuleProfile       `json:"module"`
	Bindgen            RustBindgenProfile      `json:"bindgen"`
	HostLibraries      []RustHostLibrary       `json:"host_libraries"`
	ProcMacros         []RustProcMacroProfile  `json:"proc_macros"`
	Crates             []RustCrateProfile      `json:"crates"`
	GeneratedAssembly  []RustGeneratedAssembly `json:"generated_assembly,omitempty"`
	Exports            RustExportsProfile      `json:"exports"`
	RuntimeObjects     []RustRuntimeObject     `json:"runtime_objects"`
	UnsupportedConfigs []string                `json:"unsupported_configs"`
}

type RustTargetRecipe struct {
	Kind            string `json:"kind"`
	GeneratorSource string `json:"generator_source,omitempty"`
	Stdin           string `json:"stdin,omitempty"`
	Output          string `json:"output,omitempty"`
	BuiltinTriple   string `json:"builtin_triple,omitempty"`
}

type RustVersionedFlagSet struct {
	Always            []string               `json:"always"`
	VersionPredicates []RustVersionPredicate `json:"version_predicates"`
}

type RustFlagSet struct {
	Always            []string               `json:"always"`
	Conditional       []RustConditionalFlags `json:"conditional"`
	VersionPredicates []RustVersionPredicate `json:"version_predicates"`
}

// RustVersionPredicate transforms a flag list in source order. Consumers remove
// flags before adding flags from the selected semver branch.
type RustVersionPredicate struct {
	AtLeast    string   `json:"at_least"`
	Add        []string `json:"add"`
	Remove     []string `json:"remove"`
	ElseAdd    []string `json:"else_add"`
	ElseRemove []string `json:"else_remove"`
}

// VersionPredicates transform Flags only; ElseFlags describe the false branch.
// UnlessConfig suppresses the true branch when that symbol is enabled.
type RustConditionalFlags struct {
	Config            string                 `json:"config"`
	Equals            string                 `json:"equals,omitempty"`
	UnlessConfig      string                 `json:"unless_config,omitempty"`
	Flags             []string               `json:"flags"`
	ElseFlags         []string               `json:"else_flags"`
	VersionPredicates []RustVersionPredicate `json:"version_predicates"`
}

type RustModuleProfile struct {
	AllowedFeatures   []string               `json:"allowed_features"`
	Flags             []string               `json:"flags"`
	VersionPredicates []RustVersionPredicate `json:"version_predicates"`
}

type RustBindgenProfile struct {
	Parameters     string   `json:"parameters"`
	BindingsHeader string   `json:"bindings_header"`
	UAPIHeader     string   `json:"uapi_header"`
	HelpersSource  string   `json:"helpers_source"`
	CommonFlags    []string `json:"common_flags"`
}

// RustHostLibrary is an rlib compiled for the build host and linked into
// procedural macros, such as Linux 7.x's vendored proc-macro2, quote and syn.
// Flags are appended to the common Rust flags after SkipFlags are removed.
type RustHostLibrary struct {
	Name              string                 `json:"name"`
	Source            string                 `json:"source"`
	SourcePrefixes    []string               `json:"source_prefixes"`
	Deps              []string               `json:"deps"`
	Flags             []string               `json:"flags"`
	SkipFlags         []string               `json:"skip_flags"`
	VersionPredicates []RustVersionPredicate `json:"version_predicates"`
}

// Deps name the host libraries a procedural macro links.
type RustProcMacroProfile struct {
	Name           string   `json:"name"`
	Source         string   `json:"source"`
	SourcePrefixes []string `json:"source_prefixes"`
	SourceFiles    []string `json:"source_files"`
	Deps           []string `json:"deps"`
	Flags          []string `json:"flags"`
	UsesRustcCfg   bool     `json:"uses_rustc_cfg"`
}

type RustCrateProfile struct {
	Name              string                 `json:"name"`
	Source            string                 `json:"source"`
	SourcePrefixes    []string               `json:"source_prefixes"`
	SourceFiles       []string               `json:"source_files"`
	GeneratedInputs   []string               `json:"generated_inputs"`
	Deps              []string               `json:"deps"`
	Externs           []string               `json:"externs"`
	Flags             []string               `json:"flags"`
	SkipFlags         []string               `json:"skip_flags"`
	ObjcopyFlags      []string               `json:"objcopy_flags"`
	Envs              []string               `json:"envs"`
	VersionPredicates []RustVersionPredicate `json:"version_predicates"`
}

type RustGeneratedAssembly struct {
	Config string `json:"config"`
	Equals string `json:"equals,omitempty"`
	Source string `json:"source"`
	Output string `json:"output"`
}

type RustExportsProfile struct {
	Source string   `json:"source"`
	Crates []string `json:"crates"`
}

type RustRuntimeObject struct {
	Path   string `json:"path"`
	Config string `json:"config,omitempty"`
	Equals string `json:"equals,omitempty"`
}

func (p *RustProfile) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func GenerateRustProfile(sourceRoot, arch string) (*RustProfile, error) {
	sourceRoot = filepath.Clean(sourceRoot)
	architecture, sourceArch, err := rustProfileArchitecture(arch)
	if err != nil {
		return nil, err
	}

	rootMake, err := readRustProfileFile(sourceRoot, "Makefile")
	if err != nil {
		return nil, err
	}
	archMakePath := "arch/" + sourceArch + "/Makefile"
	archMake, err := readRustProfileFile(sourceRoot, archMakePath)
	if err != nil {
		return nil, err
	}
	buildMake, err := readRustProfileFile(sourceRoot, "scripts/Makefile.build")
	if err != nil {
		return nil, err
	}
	debugMake, err := readRustProfileFile(sourceRoot, "scripts/Makefile.debug")
	if err != nil {
		return nil, err
	}
	rustMake, err := readRustProfileFile(sourceRoot, "rust/Makefile")
	if err != nil {
		return nil, err
	}

	required := []string{
		"scripts/generate_rust_target.rs",
		"rust/bindgen_parameters",
		"rust/bindings/bindings_helper.h",
		"rust/uapi/uapi_helper.h",
		"rust/helpers/helpers.c",
		"rust/exports.c",
		"rust/macros/lib.rs",
		"rust/compiler_builtins.rs",
		"rust/ffi.rs",
		"rust/build_error.rs",
		"rust/bindings/lib.rs",
		"rust/uapi/lib.rs",
		"rust/kernel/lib.rs",
	}
	for _, path := range required {
		if err := requireRustProfileFile(sourceRoot, path); err != nil {
			return nil, err
		}
	}

	commonFlags, err := makeWords(rootMake, "rust_common_flags")
	if err != nil {
		return nil, err
	}
	if err := rejectMakeWords("rust_common_flags", commonFlags); err != nil {
		return nil, err
	}
	baseFlags, err := makeWords(rootMake, "KBUILD_RUSTFLAGS")
	if err != nil {
		return nil, err
	}
	baseFlags = removeMakeWords(baseFlags, "$(rust_common_flags)")
	if err := rejectMakeWords("KBUILD_RUSTFLAGS", baseFlags); err != nil {
		return nil, err
	}
	allowedFeatures, err := makeWords(buildMake, "rust_allowed_features")
	if err != nil {
		return nil, err
	}
	if len(allowedFeatures) != 1 {
		return nil, fmt.Errorf("rust_allowed_features must contain one comma-separated value, got %q", allowedFeatures)
	}
	allowedFeatures = strings.Split(allowedFeatures[0], ",")
	for _, feature := range allowedFeatures {
		if feature == "" {
			return nil, fmt.Errorf("rust_allowed_features contains an empty feature")
		}
	}

	layout := "legacy"
	hasPinInit := rustProfileFileExists(sourceRoot, "rust/pin-init/src/lib.rs")
	hasPinInitInternal := rustProfileFileExists(sourceRoot, "rust/pin-init/internal/src/lib.rs")
	hasVendoredSyn := rustProfileFileExists(sourceRoot, "rust/syn/lib.rs")
	if hasPinInit != hasPinInitInternal {
		return nil, fmt.Errorf("unrecognized mixed Rust pin-init layout")
	}
	if hasVendoredSyn && !hasPinInit {
		return nil, fmt.Errorf("unrecognized Rust layout: vendored syn without pin-init")
	}
	if hasPinInit {
		layout = "pin-init"
		layoutFiles := []string{"rust/macros/quote.rs"}
		if hasVendoredSyn {
			// Linux 7.x replaced the hand-written quote.rs with vendored
			// proc-macro2, quote and syn host crates, and added zerocopy.
			layout = "vendored-syn"
			layoutFiles = rustVendoredSynSources
		}
		for _, path := range append(layoutFiles,
			"rust/pin-init/src/lib.rs",
			"rust/pin-init/internal/src/lib.rs",
		) {
			if err := requireRustProfileFile(sourceRoot, path); err != nil {
				return nil, err
			}
		}
		if !strings.Contains(rustMake, "obj-$(CONFIG_RUST) += bindings.o pin_init.o kernel.o") {
			return nil, fmt.Errorf("pin-init layout does not declare pin_init.o in rust/Makefile")
		}
		for _, marker := range []string{"$(obj)/pin_init.o:", "libpin_init_internal"} {
			if !strings.Contains(rustMake, marker) {
				return nil, fmt.Errorf("pin-init rust/Makefile is missing %q", marker)
			}
		}
	} else if strings.Contains(rustMake, "pin_init") {
		return nil, fmt.Errorf("legacy Rust layout references missing pin-init sources")
	}
	for _, marker := range []string{
		"obj-$(CONFIG_RUST) += core.o compiler_builtins.o ffi.o",
		"obj-$(CONFIG_RUST) += helpers/helpers.o",
		"obj-$(CONFIG_RUST) += uapi.o",
		"obj-$(CONFIG_RUST) += exports.o",
		"$(obj)/core.o:",
		"$(obj)/compiler_builtins.o:",
		"$(obj)/ffi.o:",
		"$(obj)/bindings.o:",
		"$(obj)/uapi.o:",
		"$(obj)/kernel.o:",
	} {
		if !strings.Contains(rustMake, marker) {
			return nil, fmt.Errorf("unsupported rust/Makefile crate graph: missing %q", marker)
		}
	}
	targetGenerator, err := readRustProfileFile(sourceRoot, "scripts/generate_rust_target.rs")
	if err != nil {
		return nil, err
	}
	for _, marker := range []string{"KernelConfig::from_stdin()", "CONFIG_RUSTC_VERSION"} {
		if !strings.Contains(targetGenerator, marker) {
			return nil, fmt.Errorf("unsupported Rust target generator: missing %q", marker)
		}
	}
	if architecture == "x86_64" {
		for _, marker := range []string{
			`cfg.has("X86_64")`,
			"rustc_version_atleast(1, 86, 0)",
			"rustc_version_atleast(1, 91, 0)",
			"rustc_version_atleast(1, 98, 0)",
		} {
			if !strings.Contains(targetGenerator, marker) {
				return nil, fmt.Errorf("unsupported x86 Rust target generator: missing %q", marker)
			}
		}
	} else {
		for _, marker := range []string{`cfg.has("ARM64")`, "aarch64-unknown-none"} {
			if !strings.Contains(targetGenerator, marker) {
				return nil, fmt.Errorf("unsupported arm64 Rust target generator: missing %q", marker)
			}
		}
	}
	coreEditionAtLeast, err := rustcMinimumVersion(
		rustMake,
		"core edition",
		`(?m)^core-edition[ \t]*:=[ \t]*\$\(if[ \t]+\$\(call[ \t]+rustc-min-version,[ \t]*([0-9]+)\),2024,2021\)[ \t]*$`,
	)
	if err != nil {
		return nil, err
	}
	procMacroUsesRustcCfg, err := rustProcMacroUsesRustcCfg(rustMake)
	if err != nil {
		return nil, err
	}
	if layout != "legacy" && !procMacroUsesRustcCfg {
		return nil, fmt.Errorf("pin-init Rust layout requires proc macros to consume rustc_cfg")
	}
	var vendored *rustVendoredSynGraph
	if layout == "vendored-syn" {
		vendored, err = parseRustVendoredSynGraph(rustMake, buildMake)
		if err != nil {
			return nil, err
		}
	}

	redirects, err := rustRedirectIntrinsics(rustMake, architecture)
	if err != nil {
		return nil, err
	}
	var coreObjcopy []string
	for _, symbol := range redirects {
		if strings.Contains(symbol, "$(") || strings.ContainsAny(symbol, " \t") {
			return nil, fmt.Errorf("unsupported redirect-intrinsics entry %q", symbol)
		}
		coreObjcopy = append(coreObjcopy, "--redefine-sym", symbol+"=__rust"+symbol)
	}

	target := RustTargetRecipe{}
	alwaysTargetFlags := append([]string(nil), baseFlags...)
	targetVersionPredicates := []RustVersionPredicate{}
	unsupportedConfigs := []string{}
	switch architecture {
	case "x86_64":
		target = RustTargetRecipe{
			Kind:            "generated",
			GeneratorSource: "scripts/generate_rust_target.rs",
			Stdin:           "config_auto_conf",
			Output:          "scripts/target.json",
		}
		for _, requiredFlag := range []string{
			"--target=$(objtree)/scripts/target.json",
			"-Ctarget-feature=-sse,-sse2,-sse3,-ssse3,-sse4.1,-sse4.2,-avx,-avx2",
			"-Cno-redzone=y",
			"-Ccode-model=kernel",
		} {
			if !strings.Contains(archMake, "KBUILD_RUSTFLAGS += "+requiredFlag) {
				return nil, fmt.Errorf("%s does not contain supported Rust flag %q", archMakePath, requiredFlag)
			}
			alwaysTargetFlags = append(alwaysTargetFlags, strings.ReplaceAll(requiredFlag, "$(objtree)/scripts/target.json", "{target_spec}"))
		}
	case "aarch64":
		target = RustTargetRecipe{
			Kind:          "builtin",
			BuiltinTriple: "aarch64-unknown-none",
		}
		targetAtLeast, err := rustcMinimumVersion(
			archMake,
			"arm64 soft-float target",
			`(?m)^ifeq[ \t]*\(\$\(call[ \t]+rustc-min-version,[ \t]*([0-9]+)\),y\)[ \t]*$`,
		)
		if err != nil {
			return nil, err
		}
		for _, marker := range []string{
			"KBUILD_RUSTFLAGS += --target=aarch64-unknown-none-softfloat",
			`KBUILD_RUSTFLAGS += --target=aarch64-unknown-none -Ctarget-feature="-neon"`,
		} {
			if !strings.Contains(archMake, marker) {
				return nil, fmt.Errorf("%s does not contain supported Rust target marker %q", archMakePath, marker)
			}
		}
		alwaysTargetFlags = append(alwaysTargetFlags,
			"--target=aarch64-unknown-none",
			"-Ctarget-feature=-neon",
		)
		targetVersionPredicates = append(targetVersionPredicates, newRustVersionPredicate(
			targetAtLeast,
			[]string{"--target=aarch64-unknown-none-softfloat"},
			[]string{"--target=aarch64-unknown-none", "-Ctarget-feature=-neon"},
			nil,
			[]string{"--target=aarch64-unknown-none-softfloat"},
		))
		unsupportedConfigs = []string{
			"CONFIG_CPU_BIG_ENDIAN",
			"CONFIG_CFI",
			"CONFIG_CFI_CLANG",
			"CONFIG_KASAN",
			"CONFIG_KCSAN",
			"CONFIG_UBSAN",
		}
	}
	alwaysTargetFlags = append(alwaysTargetFlags, "@{rustc_cfg}")

	conditional, err := rustTargetConditionalFlags(rootMake, debugMake, archMake, architecture)
	if err != nil {
		return nil, err
	}

	procMacros := []RustProcMacroProfile{{
		Name:           "macros",
		Source:         "rust/macros/lib.rs",
		SourcePrefixes: []string{"rust/macros/"},
		SourceFiles:    []string{},
		Flags:          []string{},
		UsesRustcCfg:   procMacroUsesRustcCfg,
	}}
	var hostLibraries []RustHostLibrary
	switch layout {
	case "pin-init":
		procMacros = append(procMacros, RustProcMacroProfile{
			Name:           "pin_init_internal",
			Source:         "rust/pin-init/internal/src/lib.rs",
			SourcePrefixes: []string{"rust/pin-init/internal/src/"},
			SourceFiles:    []string{"rust/macros/quote.rs"},
			Flags:          []string{"--cfg", "kernel"},
			UsesRustcCfg:   true,
		})
	case "vendored-syn":
		hostLibraries = vendored.hostLibraries
		procMacros = vendored.procMacros
	}

	crates := rustCrateGraph(layout, coreObjcopy, coreEditionAtLeast, vendored)
	generatedAssembly, err := rustGeneratedAssembly(sourceRoot, rustMake, layout)
	if err != nil {
		return nil, err
	}
	for i := range crates {
		if crates[i].Name == "kernel" {
			for _, generated := range generatedAssembly {
				crates[i].GeneratedInputs = append(crates[i].GeneratedInputs, generated.Output)
			}
		}
	}

	moduleExterns := []string{"kernel"}
	switch layout {
	case "pin-init":
		moduleExterns = append([]string{"pin_init"}, moduleExterns...)
	case "vendored-syn":
		moduleExterns = vendored.moduleExterns
	}
	moduleFlags := []string{
		"--cfg", "MODULE",
		"-Zallow-features={allowed_features_csv}",
		"-Zcrate-attr=no_std",
		"-Zcrate-attr=feature({allowed_features_csv})",
		"-Zunstable-options",
	}
	for _, name := range moduleExterns {
		moduleFlags = append(moduleFlags, "--extern", name)
	}
	moduleFlags = append(moduleFlags, "--crate-type", "rlib", "-L{rust_dir}", "--sysroot=/dev/null")

	runtime := []RustRuntimeObject{
		{Path: "rust/core.o"},
		{Path: "rust/compiler_builtins.o"},
		{Path: "rust/ffi.o"},
	}
	if layout == "vendored-syn" {
		runtime = append(runtime, RustRuntimeObject{Path: "rust/zerocopy.o"})
	}
	runtime = append(runtime,
		RustRuntimeObject{Path: "rust/helpers/helpers.o"},
		RustRuntimeObject{Path: "rust/bindings.o"},
	)
	if layout != "legacy" {
		runtime = append(runtime, RustRuntimeObject{Path: "rust/pin_init.o"})
	}
	bindgenRustTarget := "1.68"
	if layout == "vendored-syn" {
		bindgenRustTarget = vendored.bindgenRustTarget
	}
	runtime = append(runtime,
		RustRuntimeObject{Path: "rust/kernel.o"},
		RustRuntimeObject{Path: "rust/uapi.o"},
		RustRuntimeObject{Path: "rust/build_error.o", Config: "CONFIG_RUST_BUILD_ASSERT_ALLOW", Equals: "y"},
		RustRuntimeObject{Path: "rust/exports.o"},
	)

	profile := &RustProfile{
		Schema:       RustProfileSchema,
		Architecture: architecture,
		SourceLayout: layout,
		Target:       target,
		CommonFlags: RustVersionedFlagSet{
			Always: commonFlags,
		},
		TargetFlags: RustFlagSet{
			Always:            alwaysTargetFlags,
			Conditional:       conditional,
			VersionPredicates: targetVersionPredicates,
		},
		Module: RustModuleProfile{
			AllowedFeatures: allowedFeatures,
			Flags:           moduleFlags,
		},
		Bindgen: RustBindgenProfile{
			Parameters:     "rust/bindgen_parameters",
			BindingsHeader: "rust/bindings/bindings_helper.h",
			UAPIHeader:     "rust/uapi/uapi_helper.h",
			HelpersSource:  "rust/helpers/helpers.c",
			CommonFlags: []string{
				"--rust-target", bindgenRustTarget,
				"--use-core",
				"--with-derive-default",
				"--ctypes-prefix", "ffi",
				"--no-layout-tests",
				"--no-debug", ".*",
				"--enable-function-attribute-detection",
			},
		},
		HostLibraries:     hostLibraries,
		ProcMacros:        procMacros,
		Crates:            crates,
		GeneratedAssembly: generatedAssembly,
		Exports: RustExportsProfile{
			Source: "rust/exports.c",
			Crates: []string{"core", "helpers", "bindings", "kernel"},
		},
		RuntimeObjects:     runtime,
		UnsupportedConfigs: unsupportedConfigs,
	}
	normalizeRustProfile(profile)
	return profile, nil
}

func rustCrateGraph(layout string, coreObjcopy []string, coreEditionAtLeast string, vendored *rustVendoredSynGraph) []RustCrateProfile {
	empty := []string{}
	crates := []RustCrateProfile{
		{
			Name:            "core",
			Source:          "rustc://library/core/src/lib.rs",
			SourcePrefixes:  []string{"rustc://library/core/"},
			SourceFiles:     empty,
			GeneratedInputs: empty,
			Deps:            empty,
			Externs:         empty,
			Flags:           []string{"--cfg", "no_fp_fmt_parse"},
			SkipFlags:       []string{"-Wunreachable_pub"},
			ObjcopyFlags:    coreObjcopy,
			VersionPredicates: []RustVersionPredicate{newRustVersionPredicate(
				coreEditionAtLeast,
				[]string{"--edition=2024"},
				[]string{"--edition=2021"},
				nil,
				[]string{"--edition=2024"},
			)},
		},
		{
			Name:            "compiler_builtins",
			Source:          "rust/compiler_builtins.rs",
			SourcePrefixes:  empty,
			SourceFiles:     []string{"rust/compiler_builtins.rs"},
			GeneratedInputs: empty,
			Deps:            []string{"core"},
			Externs:         empty,
			Flags:           empty,
			SkipFlags:       empty,
			ObjcopyFlags:    []string{"-w", "-W", "__*"},
		},
	}
	if vendored != nil {
		crates = append(crates, vendored.zerocopy)
	}
	if layout != "legacy" {
		pinInitFlags := []string{"--cfg", "kernel"}
		if vendored != nil {
			pinInitFlags = vendored.pinInitFlags
		}
		crates = append(crates, RustCrateProfile{
			Name:            "pin_init",
			Source:          "rust/pin-init/src/lib.rs",
			SourcePrefixes:  []string{"rust/pin-init/src/"},
			SourceFiles:     empty,
			GeneratedInputs: empty,
			Deps:            []string{"core", "compiler_builtins", "macros", "pin_init_internal"},
			Externs:         []string{"pin_init_internal", "macros"},
			Flags:           pinInitFlags,
			SkipFlags:       empty,
			ObjcopyFlags:    empty,
		})
	}
	baseDeps := []string{"core", "compiler_builtins"}
	pinDeps := []string{}
	pinExterns := []string{}
	if layout != "legacy" {
		pinDeps = []string{"pin_init", "macros", "pin_init_internal"}
		pinExterns = []string{"pin_init"}
	}
	crates = append(crates,
		RustCrateProfile{
			Name: "ffi", Source: "rust/ffi.rs", SourcePrefixes: empty,
			SourceFiles: []string{"rust/ffi.rs"}, GeneratedInputs: empty,
			Deps: append([]string(nil), baseDeps...), Externs: empty,
			Flags: empty, SkipFlags: empty, ObjcopyFlags: empty,
		},
		RustCrateProfile{
			Name: "build_error", Source: "rust/build_error.rs", SourcePrefixes: empty,
			SourceFiles: []string{"rust/build_error.rs"}, GeneratedInputs: empty,
			Deps: append([]string(nil), baseDeps...), Externs: empty,
			Flags: empty, SkipFlags: empty, ObjcopyFlags: empty,
		},
		RustCrateProfile{
			Name: "bindings", Source: "rust/bindings/lib.rs", SourcePrefixes: []string{"rust/bindings/"},
			SourceFiles: empty, GeneratedInputs: []string{"bindings_generated", "bindings_helpers_generated"},
			Deps: append(append(append([]string{}, baseDeps...), "ffi"), pinDeps...), Externs: append([]string{"ffi"}, pinExterns...),
			Flags: empty, SkipFlags: empty, ObjcopyFlags: empty,
		},
		RustCrateProfile{
			Name: "uapi", Source: "rust/uapi/lib.rs", SourcePrefixes: []string{"rust/uapi/"},
			SourceFiles: empty, GeneratedInputs: []string{"uapi_generated"},
			Deps: append(append(append([]string{}, baseDeps...), "ffi"), pinDeps...), Externs: append([]string{"ffi"}, pinExterns...),
			Flags: empty, SkipFlags: empty, ObjcopyFlags: empty,
		},
	)
	kernelDeps := append([]string{}, baseDeps...)
	kernelDeps = append(kernelDeps, "ffi", "build_error")
	if layout != "legacy" {
		kernelDeps = append(kernelDeps, "pin_init", "bindings", "uapi", "macros", "pin_init_internal")
	} else {
		kernelDeps = append(kernelDeps, "bindings", "uapi", "macros")
	}
	kernelExterns := append([]string{"ffi"}, pinExterns...)
	kernelExterns = append(kernelExterns, "build_error", "macros", "bindings", "uapi")
	if vendored != nil {
		kernelDeps = append(kernelDeps, "zerocopy", "zerocopy_derive")
		kernelExterns = append(kernelExterns, "zerocopy", "zerocopy_derive")
	}
	crates = append(crates, RustCrateProfile{
		Name: "kernel", Source: "rust/kernel/lib.rs", SourcePrefixes: []string{"rust/kernel/"},
		SourceFiles: empty, GeneratedInputs: empty,
		Deps: kernelDeps, Externs: kernelExterns,
		Flags: empty, SkipFlags: empty, ObjcopyFlags: empty,
	})
	return crates
}

// rustVendoredSynSources are the crate roots Linux 7.x vendors for its
// procedural macros and for zerocopy.
var rustVendoredSynSources = []string{
	"rust/proc-macro2/lib.rs",
	"rust/quote/lib.rs",
	"rust/syn/lib.rs",
	"rust/zerocopy/src/lib.rs",
	"rust/zerocopy-derive/lib.rs",
}

var (
	rustMinVersionWords = regexp.MustCompile(`\$\(if[ \t]+\$\(call[ \t]+rustc-min-version,[ \t]*([0-9]+)\),([^$()]*)\)`)
	rustExternFlag      = regexp.MustCompile(`--extern[ \t]+([A-Za-z0-9_]+)`)
	rustMacrosFlags     = regexp.MustCompile(`(?m)^\$\(obj\)/\$\(libmacros_name\):[ \t]*private[ \t]+rustc_target_flags[ \t]*=[ \t]*\\\n[ \t]*([^\\\n]*)$`)
	rustBindgenTarget   = regexp.MustCompile(`\$\(BINDGEN\)[ \t]+\$<[ \t]+\$\(bindgen_target_flags\)[ \t]+--rust-target[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)
	rustCrateEnv        = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=[^ \t]*$`)
)

// rustVendoredSynGraph is the part of the Linux 7.x crate graph that differs
// from the pin-init layout.
type rustVendoredSynGraph struct {
	hostLibraries     []RustHostLibrary
	procMacros        []RustProcMacroProfile
	zerocopy          RustCrateProfile
	pinInitFlags      []string
	moduleExterns     []string
	bindgenRustTarget string
}

// rustMakeCrateFlags is one crate's <crate>-flags variable from rust/Makefile
// with its cfgs expanded and its --extern dependencies separated.
type rustMakeCrateFlags struct {
	flags      []string
	externs    []string
	skipFlags  []string
	predicates []RustVersionPredicate
}

// parseRustVendoredSynGraph reads the Linux 7.x crate recipes from
// rust/Makefile and the module externs from scripts/Makefile.build.
func parseRustVendoredSynGraph(rustMake, buildMake string) (*rustVendoredSynGraph, error) {
	for _, marker := range []string{
		"obj-$(CONFIG_RUST) += zerocopy.o",
		"always-$(CONFIG_RUST) += host/libproc_macro2.rlib host/libquote.rlib host/libsyn.rlib",
		"always-$(CONFIG_RUST) += $(libzerocopy_derive_name) $(libmacros_name) $(libpin_init_internal_name)",
		"$(obj)/zerocopy.o:",
		"cmd_rustc_procmacrolibrary =",
		"--emit=link=$@ --crate-type rlib -O",
		"--extern zerocopy --extern zerocopy_derive",
	} {
		if !strings.Contains(rustMake, marker) {
			return nil, fmt.Errorf("unsupported vendored-syn rust/Makefile: missing %q", marker)
		}
	}
	graph := &rustVendoredSynGraph{}
	hostNames := map[string]bool{}
	for _, name := range []string{"proc_macro2", "quote", "syn"} {
		parsed, err := parseRustMakeCrateFlags(rustMake, name)
		if err != nil {
			return nil, err
		}
		for _, dep := range parsed.externs {
			if !hostNames[dep] {
				return nil, fmt.Errorf("host crate %s depends on unmodeled crate %s", name, dep)
			}
		}
		directory := "rust/" + strings.ReplaceAll(name, "_", "-") + "/"
		graph.hostLibraries = append(graph.hostLibraries, RustHostLibrary{
			Name:              name,
			Source:            directory + "lib.rs",
			SourcePrefixes:    []string{directory},
			Deps:              parsed.externs,
			Flags:             parsed.flags,
			SkipFlags:         parsed.skipFlags,
			VersionPredicates: parsed.predicates,
		})
		hostNames[name] = true
	}

	macrosMatch := rustMacrosFlags.FindStringSubmatch(rustMake)
	if macrosMatch == nil {
		return nil, fmt.Errorf("unsupported vendored-syn rust/Makefile: missing macros proc-macro flags")
	}
	macrosWords := strings.Fields(macrosMatch[1])
	if err := rejectMakeWords("macros proc-macro flags", macrosWords); err != nil {
		return nil, err
	}
	macros := rustMakeCrateFlags{}
	if err := splitRustExterns("macros proc-macro flags", macrosWords, &macros); err != nil {
		return nil, err
	}
	procMacroFlags := map[string]rustMakeCrateFlags{"macros": macros}
	for _, name := range []string{"zerocopy_derive", "pin_init_internal"} {
		parsed, err := parseRustMakeCrateFlags(rustMake, name)
		if err != nil {
			return nil, err
		}
		if len(parsed.predicates) != 0 || len(parsed.skipFlags) != 0 {
			return nil, fmt.Errorf("procedural macro %s has unsupported version-gated or skipped flags", name)
		}
		procMacroFlags[name] = parsed
	}
	for _, recipe := range []struct{ name, source, prefix string }{
		{"zerocopy_derive", "rust/zerocopy-derive/lib.rs", "rust/zerocopy-derive/"},
		{"macros", "rust/macros/lib.rs", "rust/macros/"},
		{"pin_init_internal", "rust/pin-init/internal/src/lib.rs", "rust/pin-init/internal/src/"},
	} {
		parsed := procMacroFlags[recipe.name]
		for _, dep := range parsed.externs {
			if !hostNames[dep] {
				return nil, fmt.Errorf("procedural macro %s depends on unmodeled crate %s", recipe.name, dep)
			}
		}
		graph.procMacros = append(graph.procMacros, RustProcMacroProfile{
			Name:           recipe.name,
			Source:         recipe.source,
			SourcePrefixes: []string{recipe.prefix},
			SourceFiles:    []string{},
			Deps:           parsed.externs,
			Flags:          nonNilStrings(parsed.flags),
			UsesRustcCfg:   true,
		})
	}

	zerocopy, err := parseRustMakeCrateFlags(rustMake, "zerocopy")
	if err != nil {
		return nil, err
	}
	if len(zerocopy.externs) != 0 {
		return nil, fmt.Errorf("zerocopy depends on unmodeled crates %q", zerocopy.externs)
	}
	envs, err := makeWords(rustMake, "zerocopy-envs")
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		if !rustCrateEnv.MatchString(env) {
			return nil, fmt.Errorf("unsupported zerocopy-envs entry %q", env)
		}
	}
	empty := []string{}
	graph.zerocopy = RustCrateProfile{
		Name:              "zerocopy",
		Source:            "rust/zerocopy/src/lib.rs",
		SourcePrefixes:    []string{"rust/zerocopy/"},
		SourceFiles:       empty,
		GeneratedInputs:   empty,
		Deps:              []string{"core", "compiler_builtins"},
		Externs:           empty,
		Flags:             nonNilStrings(zerocopy.flags),
		SkipFlags:         nonNilStrings(zerocopy.skipFlags),
		ObjcopyFlags:      empty,
		Envs:              envs,
		VersionPredicates: zerocopy.predicates,
	}

	pinInit, err := parseRustMakeCrateFlags(rustMake, "pin_init")
	if err != nil {
		return nil, err
	}
	if strings.Join(pinInit.externs, " ") != "pin_init_internal macros" ||
		len(pinInit.predicates) != 0 || len(pinInit.skipFlags) != 0 {
		return nil, fmt.Errorf("unsupported pin_init-flags layout")
	}
	graph.pinInitFlags = nonNilStrings(pinInit.flags)

	start := strings.Index(buildMake, "rust_common_cmd =")
	if start < 0 {
		return nil, fmt.Errorf("unsupported scripts/Makefile.build: missing rust_common_cmd")
	}
	block := buildMake[start:]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	for _, match := range rustExternFlag.FindAllStringSubmatch(block, -1) {
		graph.moduleExterns = append(graph.moduleExterns, match[1])
	}
	if strings.Join(graph.moduleExterns, " ") != "pin_init kernel zerocopy zerocopy_derive" {
		return nil, fmt.Errorf("unsupported module externs %q in rust_common_cmd", graph.moduleExterns)
	}

	target := rustBindgenTarget.FindStringSubmatch(rustMake)
	if target == nil {
		return nil, fmt.Errorf("unsupported vendored-syn rust/Makefile: missing bindgen --rust-target")
	}
	graph.bindgenRustTarget = target[1]
	return graph, nil
}

func parseRustMakeCrateFlags(rustMake, crate string) (rustMakeCrateFlags, error) {
	name := crate + "-flags"
	value, err := makeValue(rustMake, name)
	if err != nil {
		return rustMakeCrateFlags{}, err
	}
	out := rustMakeCrateFlags{}
	var cfgFlags []string
	cfgCall := "$(call cfgs-to-flags,$(" + crate + "-cfgs))"
	if strings.Contains(value, cfgCall) {
		value = strings.ReplaceAll(value, cfgCall, " ")
		cfgFlags, out.predicates, err = parseRustMakeCfgs(rustMake, crate)
		if err != nil {
			return rustMakeCrateFlags{}, err
		}
	}
	words := strings.Fields(value)
	if err := rejectMakeWords(name, words); err != nil {
		return rustMakeCrateFlags{}, err
	}
	if err := splitRustExterns(name, words, &out); err != nil {
		return rustMakeCrateFlags{}, err
	}
	out.flags = append(out.flags, cfgFlags...)
	skipName := crate + "-skip_flags"
	if regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(skipName) + `[ \t]*:?=`).MatchString(rustMake) {
		out.skipFlags, err = makeWords(rustMake, skipName)
		if err != nil {
			return rustMakeCrateFlags{}, err
		}
		if err := rejectMakeWords(skipName, out.skipFlags); err != nil {
			return rustMakeCrateFlags{}, err
		}
	}
	return out, nil
}

// parseRustMakeCfgs expands a <crate>-cfgs variable to --cfg flags. A
// $(if $(call rustc-min-version,N),cfgs) term becomes a version predicate.
func parseRustMakeCfgs(rustMake, crate string) ([]string, []RustVersionPredicate, error) {
	name := crate + "-cfgs"
	value, err := makeValue(rustMake, name)
	if err != nil {
		return nil, nil, err
	}
	var predicates []RustVersionPredicate
	for _, match := range rustMinVersionWords.FindAllStringSubmatch(value, -1) {
		atLeast, err := decodeRustcVersion(match[1], name)
		if err != nil {
			return nil, nil, err
		}
		predicates = append(predicates, newRustVersionPredicate(atLeast, rustCfgFlags(strings.Fields(match[2])), nil, nil, nil))
	}
	words := strings.Fields(rustMinVersionWords.ReplaceAllString(value, " "))
	if err := rejectMakeWords(name, words); err != nil {
		return nil, nil, err
	}
	return rustCfgFlags(words), predicates, nil
}

func splitRustExterns(name string, words []string, out *rustMakeCrateFlags) error {
	for i := 0; i < len(words); i++ {
		if words[i] != "--extern" {
			out.flags = append(out.flags, words[i])
			continue
		}
		if i+1 == len(words) {
			return fmt.Errorf("%s ends with --extern", name)
		}
		i++
		out.externs = append(out.externs, words[i])
	}
	return nil
}

func rustCfgFlags(cfgs []string) []string {
	flags := make([]string, 0, 2*len(cfgs))
	for _, cfg := range cfgs {
		flags = append(flags, "--cfg", cfg)
	}
	return flags
}

func rustGeneratedAssembly(sourceRoot, rustMake, layout string) ([]RustGeneratedAssembly, error) {
	if layout == "legacy" {
		return nil, nil
	}
	entries := []RustGeneratedAssembly{
		{Config: "CONFIG_JUMP_LABEL", Equals: "y", Source: "rust/kernel/generated_arch_static_branch_asm.rs.S", Output: "rust/kernel/generated_arch_static_branch_asm.rs"},
		{Config: "CONFIG_BUG", Equals: "y", Source: "rust/kernel/generated_arch_warn_asm.rs.S", Output: "rust/kernel/generated_arch_warn_asm.rs"},
		{Config: "CONFIG_BUG", Equals: "y", Source: "rust/kernel/generated_arch_reachable_asm.rs.S", Output: "rust/kernel/generated_arch_reachable_asm.rs"},
	}
	for _, entry := range entries {
		if err := requireRustProfileFile(sourceRoot, entry.Source); err != nil {
			return nil, err
		}
		if !strings.Contains(rustMake, filepath.Base(entry.Output)) {
			return nil, fmt.Errorf("rust/Makefile does not declare generated assembly %q", entry.Output)
		}
	}
	return entries, nil
}

func rustTargetConditionalFlags(rootMake, debugMake, archMake, architecture string) ([]RustConditionalFlags, error) {
	required := []struct {
		marker string
		group  RustConditionalFlags
	}{
		{
			"KBUILD_RUSTFLAGS += -Copt-level=s",
			newRustConditionalFlags(
				"CONFIG_CC_OPTIMIZE_FOR_SIZE",
				[]string{"-Copt-level=s"},
				[]string{"-Copt-level=2"},
				nil,
			),
		},
		{
			"CONFIG_RUST_DEBUG_ASSERTIONS",
			newRustConditionalFlags(
				"CONFIG_RUST_DEBUG_ASSERTIONS",
				[]string{"-Cdebug-assertions=y"},
				[]string{"-Cdebug-assertions=n"},
				nil,
			),
		},
		{
			"CONFIG_RUST_OVERFLOW_CHECKS",
			newRustConditionalFlags(
				"CONFIG_RUST_OVERFLOW_CHECKS",
				[]string{"-Coverflow-checks=y"},
				[]string{"-Coverflow-checks=n"},
				nil,
			),
		},
		{
			"ifdef CONFIG_FRAME_POINTER",
			newRustConditionalFlags(
				"CONFIG_FRAME_POINTER",
				[]string{"-Cforce-frame-pointers=y"},
				nil,
				nil,
			),
		},
	}
	out := make([]RustConditionalFlags, 0, len(required)+8)
	for _, item := range required {
		if !strings.Contains(rootMake, item.marker) {
			return nil, fmt.Errorf("unsupported Rust flag layout: missing %q", item.marker)
		}
		out = append(out, item.group)
	}
	framePointerAtLeast, err := rustcMinimumVersion(
		rootMake,
		"frame-pointer module flag",
		`(?m)^KBUILD_RUSTFLAGS[ \t]*\+=[ \t]*\$\(if[ \t]+\$\(call[ \t]+rustc-min-version,[ \t]*([0-9]+)\),,-Zllvm_module_flag=frame-pointer:u32:2:max\)[ \t]*$`,
	)
	if err != nil {
		return nil, err
	}
	out[len(out)-1].VersionPredicates = []RustVersionPredicate{newRustVersionPredicate(
		framePointerAtLeast,
		nil,
		[]string{"-Zllvm_module_flag=frame-pointer:u32:2:max"},
		[]string{"-Zllvm_module_flag=frame-pointer:u32:2:max"},
		nil,
	)}
	if strings.Contains(rootMake, "KBUILD_RUSTFLAGS-$(CONFIG_WERROR) += -Dwarnings") {
		out = append(out, newRustConditionalFlags(
			"CONFIG_WERROR",
			[]string{"-Dwarnings"},
			nil,
			nil,
		))
	}
	for _, marker := range []string{
		"CONFIG_DEBUG_INFO",
		"DEBUG_RUSTFLAGS",
		"-Cdebuginfo=2",
		"CONFIG_DEBUG_INFO_DWARF5",
		"-Zdwarf-version=5",
	} {
		if !strings.Contains(debugMake, marker) {
			return nil, fmt.Errorf("unsupported Rust debug flag layout: missing %q", marker)
		}
	}
	out = append(out,
		newRustConditionalFlags(
			"CONFIG_DEBUG_INFO",
			[]string{"-Cdebuginfo=2"},
			nil,
			nil,
		),
		newRustConditionalFlags(
			"CONFIG_DEBUG_INFO_DWARF5",
			[]string{"-Zdwarf-version=5"},
			nil,
			nil,
		),
	)

	switch architecture {
	case "x86_64":
		return rustX86ConditionalFlags(archMake, out)
	case "aarch64":
		return rustArm64ConditionalFlags(archMake, out)
	default:
		return nil, fmt.Errorf("unsupported Rust profile architecture %q", architecture)
	}
}

func rustX86ConditionalFlags(archMake string, out []RustConditionalFlags) ([]RustConditionalFlags, error) {
	for _, marker := range []string{"RETHUNK_RUSTFLAGS", "CONFIG_X86_KERNEL_IBT", "PADDING_RUSTFLAGS"} {
		if !strings.Contains(archMake, marker) {
			return nil, fmt.Errorf("unsupported x86 Rust flag layout: missing %q", marker)
		}
	}
	out = append(out, newRustConditionalFlags(
		"CONFIG_MITIGATION_RETHUNK",
		[]string{"-Zfunction-return=thunk-extern"},
		nil,
		nil,
	))
	jumpTablesAtLeast, err := rustcMinimumVersion(
		archMake,
		"x86 IBT jump-table flag",
		`(?m)^KBUILD_RUSTFLAGS[ \t]*\+=[ \t]*-Zcf-protection=branch[ \t]+\$\(if[ \t]+\$\(call[ \t]+rustc-min-version,[ \t]*([0-9]+)\),-Cjump-tables=n,-Zno-jump-tables\)[ \t]*$`,
	)
	if err != nil {
		return nil, err
	}
	out = append(out, newRustConditionalFlags(
		"CONFIG_X86_KERNEL_IBT",
		[]string{"-Zcf-protection=branch"},
		nil,
		[]RustVersionPredicate{newRustVersionPredicate(
			jumpTablesAtLeast,
			[]string{"-Cjump-tables=n"},
			[]string{"-Zno-jump-tables"},
			[]string{"-Zno-jump-tables"},
			[]string{"-Cjump-tables=n"},
		)},
	))
	out = append(out, newRustConditionalFlags(
		"CONFIG_CALL_PADDING",
		[]string{"-Zpatchable-function-entry={CONFIG_FUNCTION_PADDING_BYTES},{CONFIG_FUNCTION_PADDING_BYTES}"},
		nil,
		nil,
	))

	if strings.Contains(archMake, "ifdef CONFIG_X86_NATIVE_CPU") {
		if !strings.Contains(archMake, "KBUILD_RUSTFLAGS += -Ctarget-cpu=native") ||
			!strings.Contains(archMake, "KBUILD_RUSTFLAGS += -Ctarget-cpu=x86-64 -Ztune-cpu=generic") {
			return nil, fmt.Errorf("unsupported CONFIG_X86_NATIVE_CPU Rust flag layout")
		}
		out = append(out, newRustConditionalFlags(
			"CONFIG_X86_NATIVE_CPU",
			[]string{"-Ctarget-cpu=native"},
			[]string{"-Ctarget-cpu=x86-64", "-Ztune-cpu=generic"},
			nil,
		))
		return out, nil
	}

	re := regexp.MustCompile(`(?m)^[ \t]*rustflags-\$\(CONFIG_([A-Za-z0-9_]+)\)[ \t]*\+=[ \t]*(\S(?:.*\S)?)?[ \t]*$`)
	matches := re.FindAllStringSubmatch(archMake, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("arch/x86/Makefile has no supported Rust CPU flag mapping")
	}
	for _, match := range matches {
		flags := strings.Fields(match[2])
		if err := rejectMakeWords("x86 Rust CPU flags", flags); err != nil {
			return nil, err
		}
		out = append(out, newRustConditionalFlags(
			"CONFIG_"+match[1],
			flags,
			nil,
			nil,
		))
	}
	return out, nil
}

func rustArm64ConditionalFlags(archMake string, out []RustConditionalFlags) ([]RustConditionalFlags, error) {
	for _, marker := range []string{
		"CONFIG_UNWIND_TABLES",
		"KBUILD_RUSTFLAGS += -Cforce-unwind-tables=n",
		"KBUILD_RUSTFLAGS += -Cforce-unwind-tables=y -Zuse-sync-unwind=n",
		"CONFIG_ARM64_BTI_KERNEL",
		"-Zbranch-protection=bti,pac-ret",
		"CONFIG_ARM64_PTR_AUTH_KERNEL",
		"-Zbranch-protection=pac-ret",
		"CONFIG_SHADOW_CALL_STACK",
		"KBUILD_RUSTFLAGS += -Zfixed-x18",
	} {
		if !strings.Contains(archMake, marker) {
			return nil, fmt.Errorf("unsupported arm64 Rust flag layout: missing %q", marker)
		}
	}
	unwindAtLeast, err := rustcMinimumVersion(
		archMake,
		"arm64 unwind module flag",
		`(?m)^KBUILD_RUSTFLAGS[ \t]*\+=[ \t]*\$\(if[ \t]+\$\(call[ \t]+rustc-min-version,[ \t]*([0-9]+)\),,-Zllvm_module_flag=uwtable:u32:2:max\)[ \t]*$`,
	)
	if err != nil {
		return nil, err
	}
	out = append(out, newRustConditionalFlags(
		"CONFIG_UNWIND_TABLES",
		[]string{"-Cforce-unwind-tables=y", "-Zuse-sync-unwind=n"},
		[]string{"-Cforce-unwind-tables=n"},
		[]RustVersionPredicate{newRustVersionPredicate(
			unwindAtLeast,
			nil,
			[]string{"-Zllvm_module_flag=uwtable:u32:2:max"},
			[]string{"-Zllvm_module_flag=uwtable:u32:2:max"},
			nil,
		)},
	))
	ptrAuth := newRustConditionalFlags(
		"CONFIG_ARM64_PTR_AUTH_KERNEL",
		[]string{"-Zbranch-protection=pac-ret"},
		nil,
		nil,
	)
	ptrAuth.UnlessConfig = "CONFIG_ARM64_BTI_KERNEL"
	// Preserve the Makefile's if/else-if relationship: BTI includes PAC, so
	// the pointer-authentication-only flag must not also be emitted.
	out = append(out,
		newRustConditionalFlags(
			"CONFIG_ARM64_BTI_KERNEL",
			[]string{"-Zbranch-protection=bti,pac-ret"},
			nil,
			nil,
		),
		ptrAuth,
		newRustConditionalFlags(
			"CONFIG_SHADOW_CALL_STACK",
			[]string{"-Zfixed-x18"},
			nil,
			nil,
		),
	)
	return out, nil
}

func rustProcMacroUsesRustcCfg(rustMake string) (bool, error) {
	start := strings.Index(rustMake, "cmd_rustc_procmacro =")
	if start < 0 {
		return false, fmt.Errorf("unsupported rust/Makefile: missing cmd_rustc_procmacro")
	}
	block := rustMake[start:]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	return strings.Contains(block, "@$(objtree)/include/generated/rustc_cfg"), nil
}

func rustProfileArchitecture(arch string) (architecture, sourceArch string, err error) {
	switch arch {
	case "x86", "x86_64":
		return "x86_64", "x86", nil
	case "arm64", "aarch64":
		return "aarch64", "arm64", nil
	default:
		return "", "", fmt.Errorf("Rust profile generation supports only x86_64 and arm64, got ARCH=%q", arch)
	}
}

func rustRedirectIntrinsics(rustMake, architecture string) ([]string, error) {
	redirects, err := makeWords(rustMake, "redirect-intrinsics")
	if err != nil {
		return nil, err
	}
	if architecture != "aarch64" {
		return redirects, nil
	}
	for _, marker := range []string{
		"ifneq ($(or $(CONFIG_ARM64)",
		"__ashrti3",
		"__ashlti3",
		"__lshrti3",
	} {
		if !strings.Contains(rustMake, marker) {
			return nil, fmt.Errorf("unsupported arm64 redirect-intrinsics layout: missing %q", marker)
		}
	}
	return append(redirects, "__ashrti3", "__ashlti3", "__lshrti3"), nil
}

func rustcMinimumVersion(content, context, pattern string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("compile %s rustc-version pattern: %w", context, err)
	}
	match := re.FindStringSubmatch(content)
	if len(match) != 2 {
		return "", fmt.Errorf("unsupported %s layout: missing rustc minimum-version predicate", context)
	}
	return decodeRustcVersion(match[1], context)
}

// decodeRustcVersion converts Kbuild's rustc-min-version encoding, such as
// 108800, to a semver string.
func decodeRustcVersion(value, context string) (string, error) {
	encoded, err := strconv.Atoi(value)
	if err != nil {
		return "", fmt.Errorf("parse %s rustc minimum version %q: %w", context, value, err)
	}
	major := encoded / 100000
	minor := (encoded / 100) % 1000
	patch := encoded % 100
	if major < 1 || minor > 999 || patch > 99 {
		return "", fmt.Errorf("unsupported %s rustc minimum version %q", context, value)
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func newRustVersionPredicate(atLeast string, add, remove, elseAdd, elseRemove []string) RustVersionPredicate {
	return RustVersionPredicate{
		AtLeast:    atLeast,
		Add:        nonNilStrings(add),
		Remove:     nonNilStrings(remove),
		ElseAdd:    nonNilStrings(elseAdd),
		ElseRemove: nonNilStrings(elseRemove),
	}
}

func newRustConditionalFlags(config string, flags, elseFlags []string, predicates []RustVersionPredicate) RustConditionalFlags {
	return RustConditionalFlags{
		Config:            config,
		Equals:            "y",
		Flags:             nonNilStrings(flags),
		ElseFlags:         nonNilStrings(elseFlags),
		VersionPredicates: nonNilRustVersionPredicates(predicates),
	}
}

func normalizeRustProfile(profile *RustProfile) {
	profile.CommonFlags.Always = nonNilStrings(profile.CommonFlags.Always)
	profile.CommonFlags.VersionPredicates = nonNilRustVersionPredicates(profile.CommonFlags.VersionPredicates)
	profile.TargetFlags.Always = nonNilStrings(profile.TargetFlags.Always)
	profile.TargetFlags.VersionPredicates = nonNilRustVersionPredicates(profile.TargetFlags.VersionPredicates)
	for i := range profile.TargetFlags.Conditional {
		condition := &profile.TargetFlags.Conditional[i]
		condition.Flags = nonNilStrings(condition.Flags)
		condition.ElseFlags = nonNilStrings(condition.ElseFlags)
		condition.VersionPredicates = nonNilRustVersionPredicates(condition.VersionPredicates)
	}
	profile.Module.AllowedFeatures = nonNilStrings(profile.Module.AllowedFeatures)
	profile.Module.Flags = nonNilStrings(profile.Module.Flags)
	profile.Module.VersionPredicates = nonNilRustVersionPredicates(profile.Module.VersionPredicates)
	if profile.HostLibraries == nil {
		profile.HostLibraries = []RustHostLibrary{}
	}
	for i := range profile.HostLibraries {
		library := &profile.HostLibraries[i]
		library.SourcePrefixes = nonNilStrings(library.SourcePrefixes)
		library.Deps = nonNilStrings(library.Deps)
		library.Flags = nonNilStrings(library.Flags)
		library.SkipFlags = nonNilStrings(library.SkipFlags)
		library.VersionPredicates = nonNilRustVersionPredicates(library.VersionPredicates)
	}
	for i := range profile.ProcMacros {
		profile.ProcMacros[i].Deps = nonNilStrings(profile.ProcMacros[i].Deps)
	}
	for i := range profile.Crates {
		crate := &profile.Crates[i]
		crate.Envs = nonNilStrings(crate.Envs)
		crate.VersionPredicates = nonNilRustVersionPredicates(crate.VersionPredicates)
	}
	profile.UnsupportedConfigs = nonNilStrings(profile.UnsupportedConfigs)
}

func nonNilRustVersionPredicates(predicates []RustVersionPredicate) []RustVersionPredicate {
	if predicates == nil {
		return []RustVersionPredicate{}
	}
	for i := range predicates {
		predicates[i].Add = nonNilStrings(predicates[i].Add)
		predicates[i].Remove = nonNilStrings(predicates[i].Remove)
		predicates[i].ElseAdd = nonNilStrings(predicates[i].ElseAdd)
		predicates[i].ElseRemove = nonNilStrings(predicates[i].ElseRemove)
	}
	return predicates
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func readRustProfileFile(root, path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", fmt.Errorf("read Rust profile input %s: %w", path, err)
	}
	return string(data), nil
}

func requireRustProfileFile(root, path string) error {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return fmt.Errorf("Rust profile requires %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Rust profile input %s is not a regular file", path)
	}
	return nil
}

func rustProfileFileExists(root, path string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && info.Mode().IsRegular()
}

func makeWords(content, name string) ([]string, error) {
	value, err := makeValue(content, name)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(value)
	if len(words) == 0 {
		return nil, fmt.Errorf("%s assignment is empty", name)
	}
	return words, nil
}

// makeValue returns the first assignment to a Make variable with its
// continuation lines joined.
func makeValue(content, name string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	prefix := regexp.MustCompile(`^(?:export[ \t]+)?` + regexp.QuoteMeta(name) + `[ \t]*(?::=|\+=|=)[ \t]*(.*)$`)
	for i, line := range lines {
		match := prefix.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		value := match[1]
		for strings.HasSuffix(strings.TrimSpace(value), `\`) {
			value = strings.TrimSpace(value)
			value = strings.TrimSpace(strings.TrimSuffix(value, `\`))
			i++
			if i >= len(lines) {
				return "", fmt.Errorf("unterminated %s assignment", name)
			}
			value += " " + strings.TrimSpace(lines[i])
		}
		return value, nil
	}
	return "", fmt.Errorf("missing %s assignment", name)
}

func removeMakeWords(words []string, removed string) []string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		if word != removed {
			out = append(out, word)
		}
	}
	return out
}

func rejectMakeWords(name string, words []string) error {
	for _, word := range words {
		if strings.Contains(word, "$(") || strings.Contains(word, "${") || strings.ContainsAny(word, "\r\n") {
			return fmt.Errorf("%s contains unsupported Make expression %q", name, word)
		}
	}
	return nil
}
