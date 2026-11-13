# ulog v1.0.4: file rotation and JSON formatter fixes

Written 2026-11-13 from the impost repo
(`/Users/vitiok78/dev/GolandProjects/impost`, todo
`docs/ai/todo/ulog-rotation-retention.md`). impost vendors ulog v1.0.3 and
hit the first bug on staging; the other two came from a Codex review of
impost on 2026-08-21. Line numbers below are for `main` at `bef41e3`
(= v1.0.3 plus a `.gitignore` commit).

## Status (2026-11-13)

Code done on branch `fix-rotation`: bugs 1–3 and the three read-through
findings, with tests; `go test ./...` green. Not yet merged, tagged or pushed;
impost side not started.

Decisions taken:

- Ranking and `maxAge` use ModTime; the name only identifies an archive and
  breaks ties (timestamp, then counter). First done by name timestamp; the
  second Codex review showed local names with no offset run backwards after
  a DST fall-back, so the newest archives got deleted. UTC names were the
  other option, rejected: a format change, and old local-time archives would
  misorder right after the upgrade.
- Archive names gain a `.<n>` counter (n ≥ 1) when the name is taken; such
  files count as archives and rank after the plain one of the same second.
  The counter goes past the highest one of that second and never fills a gap
  (Codex review: with retention on, reusing a freed lower name ranked the
  newest archive last and deleted it).
- Rename fails: the record is written to the active file and `Write` reports
  nothing (returning an error with `n == len(p)` would break `bufio`-style
  callers, which latch the first error). `size` restarts at 0, so the next
  attempt comes after another `maxSize` bytes, not on every write.
- Reopen fails: `Write` returns the error (that record is lost), `w.file` is
  nil, the next `Write` opens again. Close errors during rotation are ignored.
- `rotate` deliberately ignores `cleanOldBackups`' error (retention must not
  stop logging; the next rotation retries). Tests call it directly.
- New: an empty file is never rotated, so a record larger than `maxSize`
  no longer leaves an empty archive. `TestFileWriterRotation` and
  `TestFileWriterCleanOldBackups` now write twice.
- Formatters: a failing value becomes its `String()`/`Error()` text, else
  its `%+v` dump. The first Codex review flagged that `%+v` prints
  `json:"-"` and unexported fields, so secrets can leak; it was briefly
  type-name-only. User decision 2026-11-13: logs must never lose content,
  the secret risk is accepted, so `%+v` is back (README says so,
  `TestFormattersDumpUnencodableStructs` pins it). fmt has no limits (it
  loops forever on cycles and can print gigabytes from a small value), and
  three Codex reviews in a row found ways past an up-front size estimate
  (scalar slices, shared strings, nested `Error()`: 9–14 GiB in tests). So
  the dump goes through our own printer (`dumper`): output identical to
  `%+v` for ordinary values (`TestDumpMatchesFmt`), `[cycle]` where fmt
  loops, `[max depth]` past 256 levels, and a hard 4 MiB cap that keeps the
  beginning and appends `...[truncated at 4194304 bytes]`. Seventh review:
  the printer still let fmt run Error/String (fmt formats a panic value with
  no limits: a self-containing panic value overflowed the stack, fatal), and
  re-looked-up map values by key (NaN keys lost their value). Now methods
  are called directly, `Format` gets a capped `fmt.State`, a panic becomes
  `%!v(PANIC=<kind> method: <string payload or its type>)`, and map entries
  come from `MapRange`. Nested `map[string]interface{}` values
  (the ctx group) degrade per entry, with a cycle marker. They are rebuilt
  as a safe tree and marshalled once (third Codex review: re-encoding each
  level as `RawMessage` copied a deep subtree once per level). A map
  referenced more than once is rebuilt once and reused; extra copies are
  written until 4 MiB of repeats, then replaced by a marker (fourth Codex
  review: 31 maps referencing the one below twice expanded to 2^30 maps
  and exhausted memory). The first copy is always written in full.
- Archive name timestamps are parsed as UTC, only to recognise names and
  break ties (third Codex review: parsing in `time.Local` rejected names in
  the local DST gap, and those archives escaped retention for good).
- `go test -race` fails on `TestMultiWriterConcurrentWrite`, also on `main`:
  out of scope, see `docs/ai/todo/multiwriter-concurrency.md`.

## Goal

Release v1.0.4 with the three bugs below fixed and covered by tests, without
changing the public API (`NewFileWriter(path, maxSize, maxBackups, maxAge)`,
`Formatter`, `JSONFormatter`, `TextFormatter`). Then bump impost and delete
its workaround.

## Bug 1 (high): `maxBackups` keeps the oldest archives and deletes the newest

`file_writer.go`, `cleanOldBackups` (lines 120–161).

- Archives are named `<path>.<20060102-150405>`. `os.ReadDir` returns names
  sorted ascending, so the slice is oldest first.
- The comment on line 139 says "Sort backups by modification time (newest
  first)", but there is no sort.
- The loop on lines 143–145 removes everything from index `maxBackups` on,
  that is the newest archives.

Seen for real: impost staging, 2026-11-12, a 700 000-request run. Only the
first five archives survived; every later one was deleted right after it
was made.

Two more defects in the same function:

- The match on lines 133–134 is a bare prefix test. Any file whose name
  starts with the log's base name counts as an archive: `stage.log.keep-me`
  or `stage.log.gz` would be counted and may be deleted.
- `os.Remove` errors are ignored, and so is the function's own error at the
  call site (line 113).

Fix:

1. Recognise an archive only when the suffix after `<base>.` parses as the
   timestamp layout (`time.Parse("20060102-150405", suffix)`).
2. Sort archives by that timestamp, newest first, then remove those past
   `maxBackups`.
3. Apply `maxAge` to the same strict list (keep using ModTime or switch to
   the parsed timestamp; pick one and say so in the doc comment).
4. Return the first removal error; the caller may ignore it, but tests can
   see it.

Tests:

- Rotate more than `maxBackups` times; assert the survivors are the newest
  `maxBackups` archives. The existing `TestFileWriterCleanOldBackups`
  (line 272) passes today, so check what it asserts and why it missed this.
- A foreign file `<base>.keep-me` in the directory survives and is not
  counted.
- `maxAge` and `maxBackups` together.

## Bug 2 (high): one failed rotation turns file logging off for good

`file_writer.go`, `rotate` (lines 89–117).

`rotate` closes the active file first (line 91). If the rename (line 99) or
the reopen (line 104) then fails, it returns with `w.file` still pointing at
the closed file. Every later `Write` fails on that closed descriptor.
`log/slog` drops handler errors, so the service keeps running and logs
nothing, with no sign of it.

Fix: no failure path may leave the writer without a usable file.

- Rename fails: reopen the original path for append and keep writing to it;
  return the error for this one write only, or write the record and report
  nothing. Decide which and document it; the writer must work on the next
  call either way.
- Reopen fails after a successful rename: retry the open on the next
  `Write` instead of using a closed handle (keep `w.file == nil` and open
  lazily).
- Close fails: still try to continue with a fresh handle.

Tests: inject the failures and assert that a later write succeeds and lands
in the file. Making the directory read-only is the cheap way to fail the
rename; a small seam for the three file operations (function fields on the
writer, unexported) makes close and open failures testable too.

## Bug 3 (medium): one value JSON cannot encode drops the whole record

`formatter.go`, `JSONFormatter.Format` (lines 41–45).

The whole record goes through one `json.Marshal`. A single context or extra
value it rejects (a channel, a func, a cyclic structure, a `NaN` float)
fails the record, and slog swallows the error. The line is lost.

`TextFormatter.Format` (lines 73–90) has the quiet version of the same
thing: on a marshal error the context or extra block is left out of the
line without a trace.

Fix: degrade per value, not per record. Marshal `Context` and `Extra` entry
by entry; a value that fails becomes its `fmt.Sprintf("%+v", v)` string
(guard that call against a panic too). The record is always written. Do the
same in the text formatter.

Tests: a record with a `chan int` and a `func()` among normal attributes
comes out as valid JSON holding the other attributes unchanged and a string
for the bad ones; same for the text formatter.

## Found while reading the source for this plan (not reproduced)

Read-through findings of 2026-11-13, no test written for them. Confirm each
before fixing; they can go into v1.0.4 or wait.

- **Two rotations in the same second overwrite an archive.** The archive
  name has one-second resolution (line 96). A second rotation within that
  second renames the active file onto the existing archive, and `os.Rename`
  replaces it. A small `maxSize` under load loses a full file of logs.
  Possible fix: add a counter or sub-second part when the name exists.
- **`Write` after `Close` panics.** `Close` sets `w.file = nil` (line 82);
  `Write` then calls `w.file.Write` (line 70) on nil. Return
  `os.ErrClosed` instead.
- **`rotate` ignores the result of `cleanOldBackups`** (line 113). Harmless
  once bug 1 is fixed, but worth a deliberate decision.

## Release and the impost side

1. Fix, test (`go test ./...`), tag `v1.0.4`, push the tag.
2. In impost: `go get github.com/vitiok78/ulog@v1.0.4`, then
   `go mod tidy && go mod vendor`.
3. impost carries a workaround for bug 1 in `internal/app/logger.go`: it
   passes `maxBackups=0` to ulog and prunes to the newest five archives
   itself (`rotatingFile`, `pruneBackups`). Its test
   `TestULogMaxBackupsDeletesNewest` pins the bug and will fail after the
   bump. That failure is the signal: delete the test, `rotatingFile` and
   `pruneBackups`, and pass `logFileMaxBackups` to `ulog.NewFileWriter`
   again.
4. impost also stringifies recovered panic values before logging them (a
   mitigation for bug 3). It can stay.
5. Move impost's `docs/ai/todo/ulog-rotation-retention.md` to its archive
   with the release noted.

## Out of scope

- Compressing archives, rotation by time, a new constructor signature.
- The other vendoring projects, if any: check who else imports ulog before
  tagging, in case one relies on the prefix match.
