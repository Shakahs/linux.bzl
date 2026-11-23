"""Sources of the Kconfig graph generator, watched by the repository rules that
build and run it."""

visibility("//...")

# Packages compiled into kconfig_parse.
_GENERATOR_SOURCE_TREES = [
    "internal/cmd/kconfig_parse",
    "internal/kconfig",
    "internal/rusttoolchain",
]

def watch_kconfig_generator_sources(rctx):
    """Re-runs the calling repository rule when the generator sources change.

    Bazel does not track a file that a repository rule reads from another
    generated repository, so a rule that runs the generator built by
    kconfig_generator watches the generator's sources itself.

    Returns:
      The linux.bzl module root.
    """
    module_root = rctx.path(Label("//:go.mod")).dirname
    rctx.watch(module_root.get_child("go.mod"))
    rctx.watch(module_root.get_child("go.sum"))
    for tree in _GENERATOR_SOURCE_TREES:
        rctx.watch_tree(module_root.get_child(tree))
    return module_root
