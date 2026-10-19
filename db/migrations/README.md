# Migrations after the baseline

`../sql/schema.sql` is the whole schema as of migration 0032 (the original
0001–0032, combined). Schema changes since go here as
`NNNN_description.sql`, numbered from `0033`, one change per file. The server
applies each one not yet recorded in `schema_migrations` at startup, in its
own transaction; never edit one that has been applied, and never edit
`schema.sql` for a change (it stays the baseline).

When a change also alters what `../sql/drop.sql` must drop, update that too.
