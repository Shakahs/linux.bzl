"""Source-tree include directories of Linux object rules.

The graph generator passes each include directory that a Kbuild flag names
inside the Linux tree as a label of the source repository's directory, so the
compile command takes the directory's path from its File rather than from a
path string.
"""

visibility("//...")

# Attributes with the compiler option each stands for.
_SOURCE_INCLUDE_DIRECTORY_ATTRS = [
    ("include_directories", "-I"),
    ("quote_include_directories", "-iquote"),
    ("system_include_directories", "-isystem"),
    ("after_include_directories", "-idirafter"),
]

def source_include_directory_attrs(prefix = ""):
    """Returns the include-directory label attributes named with prefix."""
    return {
        prefix + attr_name: attr.label_list(
            allow_files = True,
            doc = "Source directories passed to the compiler with %s, in order." % option,
        )
        for attr_name, option in _SOURCE_INCLUDE_DIRECTORY_ATTRS
    }

def source_include_directory_flags(ctx, prefix = ""):
    """Returns the include flags for the directories labeled under prefix.

    The directories are not action inputs: the headers an object reads arrive
    through its source input group.
    """
    flags = []
    for attr_name, option in _SOURCE_INCLUDE_DIRECTORY_ATTRS:
        for directory in getattr(ctx.files, prefix + attr_name):
            flags.extend([option, directory.path])
    return flags
