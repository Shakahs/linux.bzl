"""Builds the Kconfig graph generator from this module's own sources.

Repository rules cannot consume build actions, so the generator that turns a
Linux tree and config into the per-object Bazel graph is compiled here, at
repository time, with rules_go's host-compatible Go SDK. Its Go module
dependencies are fetched through the Go module proxy and verified against
this module's go.sum. A change to the generator sources rebuilds it in the
same Bazel invocation; no prebuilt release archive is involved.
"""

load("@go_host_compatible_sdk_label//:defs.bzl", "HOST_COMPATIBLE_SDK")
load(":kconfig_generator_sources.bzl", "watch_kconfig_generator_sources")

visibility("//...")

def _go_binary(rctx):
    sdk_root = rctx.path(HOST_COMPATIBLE_SDK).dirname
    suffix = ".exe" if "windows" in rctx.os.name.lower() else ""
    go = sdk_root.get_child("bin").get_child("go" + suffix)
    if not go.exists:
        fail("rules_go host-compatible Go SDK has no go binary at %s" % go)
    return go

def _kconfig_generator_impl(rctx):
    # Edits to the generator sources rebuild the tool.
    module_root = watch_kconfig_generator_sources(rctx)

    suffix = ".exe" if "windows" in rctx.os.name.lower() else ""
    output = rctx.path("kconfig_parse" + suffix)
    scratch = rctx.path(".go")
    result = rctx.execute(
        [
            str(_go_binary(rctx)),
            "build",
            "-trimpath",
            "-buildvcs=false",
            "-o",
            str(output),
            "./internal/cmd/kconfig_parse",
        ],
        environment = {
            "CGO_ENABLED": "0",
            "GOCACHE": str(scratch.get_child("cache")),
            "GOENV": "off",
            # -mod=readonly keeps go.mod and go.sum authoritative; -modcacherw
            # lets the scratch module cache be deleted afterwards.
            "GOFLAGS": "-mod=readonly -modcacherw",
            "GOMODCACHE": str(scratch.get_child("modcache")),
            "GOPATH": str(scratch.get_child("path")),
            "GOPROXY": "https://proxy.golang.org",
            "GOTOOLCHAIN": "local",
            # The module may sit inside a consumer's Go workspace checkout.
            "GOWORK": "off",
            "HOME": str(scratch),
        },
        quiet = False,
        timeout = 1800,
        working_directory = str(module_root),
    )
    if result.return_code != 0:
        fail("building the linux.bzl Kconfig graph generator failed:\n%s\n%s" % (result.stdout, result.stderr))
    rctx.delete(scratch)
    rctx.file(
        "BUILD.bazel",
        'exports_files(["kconfig_parse%s"])\n' % suffix,
        executable = False,
    )

kconfig_generator = repository_rule(
    implementation = _kconfig_generator_impl,
    doc = "Compiles kconfig_parse from linux.bzl's sources for repository-time graph generation.",
)
