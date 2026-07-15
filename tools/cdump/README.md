# cdump

Generates golden test fixtures from the pinned FFmpeg tree
(d09d5afc3aebede25d2d245ee23b75a47ea17c3a). It links against the prebuilt
static libs in the out-of-tree build directory (go-aac-ffbuild); see the
Phase 0b plan for the exact macOS clang command. Fixtures are committed
under internal/*/testdata/; rerun only if the pin changes.
