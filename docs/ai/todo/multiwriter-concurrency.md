# MultiWriter and Handler do not serialize writes

**Status:** OPEN (found 2026-11-13 while executing the v1.0.4 plan)

`go test -race ./...` fails on `TestMultiWriterConcurrentWrite`, on `main` too
(not caused by v1.0.4). `MultiWriter.Write` (`writer.go`) has no lock, and
`Handler.Handle` (`handler.go`) calls `Config.Writer.Write` without one, so
concurrent log calls over a writer that is not itself safe for concurrent use
(`bytes.Buffer`, a `bufio.Writer`, a MultiWriter over those) race.
`FileWriter` and `*os.File` are safe and unaffected.

Options: a mutex in `MultiWriter.Write`, or one in `Handler` around the write
(shared across `WithAttrs`/`WithGroup` copies, since they share the writer).
Then the test passes under `-race` and can go in CI.
