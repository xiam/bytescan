# Guide: fixed-size packets with a sync marker

An MPEG transport stream is a run of 188-byte packets, and every packet starts
with the sync byte 0x47. The size is fixed, so no length is read; the sync
byte is how a reader finds the packets again after corrupt data.

## The scanners

Run three scanners on a `modes.Driver` over one `bytesource.NewCursor`, and a
`bytesource.Fields` queue:

- **In sync:** a sync scanner with one rule, `octet.Set(0x47)`. Any other byte
  is `UNKNOWN`, and the driver has lost sync.
- **Header:** a header scanner with one rule, `octet.Fixed(3)`. The driver reads
  the header bits, queues the rest of the packet as fields (an adaptation
  field when the header announces one, then the payload), and goes back in
  sync at the next packet.
- **Lost:** a scanner that accepts a 0x47 only when the byte one packet later is
  0x47 too. It finds the next real packet.

Each scanner has one rule, so no registration order is needed.

## Confirm a sync byte before you trust it

0x47 is also a common byte inside a payload. After lost sync, the first 0x47
is probably not a packet start. `core.FollowedBy(rule, ahead)` matches `rule`
only when `ahead` matches after it, and it does not consume `ahead`:

```go
const size = 4 // the packet size, sync byte included
in := []byte{
	0x01, 0x47, 0x02, // noise, with a 0x47 that no packet confirms
	0x47, 0xA0, 0xA1, 0xA2, // a packet
	0x47, // the next packet starts
}

// A 0x47 is SYNC only when the byte one packet later is 0x47 too.
sync := octet.Set(0x47)
lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(in)))
lx.MustAddRule("SYNC", core.FollowedBy(sync, core.Sequence(octet.Fixed(size-1), sync)))

for {
	l, err := lx.Next()
	if err == io.EOF {
		break
	}
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(bytesource.Hex(l))
	if l.Type() == "SYNC" {
		break // a driver now switches to its header scanner
	}
}
```

The code is `Example_guideSync` in
[`example_guides_test.go`](../../example_guides_test.go), with packets of 4
bytes. The 0x47 at offset 1 is not confirmed, because the byte at offset 5 is
not 0x47. The one at offset 3 is.

## Traps

- **The look-ahead reads a whole packet.** The confirmed sync of a 188-byte
  packet reads 189 bytes. Set the token guard above that: twice
  `streamtest.ByteGuard` covers it, because the guard counts a byte of 0x80 or
  above as two bytes.
- **A 0x47 that the input ends before confirming is `UNKNOWN`.** Near the end
  of a stream, the last packet start cannot be confirmed by the lost scanner. In
  sync, the sync scanner does not need to confirm, so a clean stream lexes to
  its last packet.
- **Read the fields, do not lex them.** The payload is opaque bytes. Queue it
  as a `bytesource.Field` and let the driver read it; release (or switch away
  from) the header scanner first.
- **Do not parse the fields.** The driver uses the adaptation field length
  only to frame the field.

## Tests to write

- A golden test in hex with clean packets, a packet with an adaptation field,
  a packet cut by the end of the input, and noise before the first packet.
- A corruption test with `streamtest.Corrupt` that checks the driver is back
  in sync at the first packet after each garbage run.
- A false-sync case: a 0x47 in the noise whose byte one packet later is not
  0x47.

## Examples

[mpegts](https://github.com/xiam/bytescan-examples/tree/main/streams/mpegts): fixed-size packets that start
with a sync byte; a look-ahead rule confirms the sync.
