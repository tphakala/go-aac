# gentables

Emits the AAC codec tables of the pinned FFmpeg tree
(d09d5afc3aebede25d2d245ee23b75a47ea17c3a) as Go source
(internal/tables/tables_gen.go) and as a binary golden fixture
(internal/tables/testdata/ctables.bin). It #includes the pinned table
translation units directly (aactab.c, aacenctab.c, aacpsy.c), so file-local
static tables are emitted with compiler-verified lengths, and links against
the prebuilt libavutil.a only. Run via `go generate ./internal/tables`
(generate.sh verifies the pin first). Both generated artifacts are
committed; rerun only if the pin changes.
