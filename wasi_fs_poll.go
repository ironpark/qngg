package qngg

import (
	"encoding/binary"
	"io"
	"time"
)

func (s *wasiFS) clock(id uint64) (uint64, uint32) {
	switch id {
	case 0: // realtime
		return uint64(time.Now().UnixNano()), 0
	case 1: // monotonic; WASI leaves its epoch unspecified
		return uint64(time.Since(s.clockStart)), 0
	default:
		return 0, wasiInval
	}
}

func (c *wasiFSCall) clockTime() uint32 {
	b := c.bytes(c.args[2], 8)
	if b == nil {
		return wasiFault
	}

	now, errno := c.fs.clock(c.args[0])
	if errno != 0 {
		return errno
	}

	binary.LittleEndian.PutUint64(b, now)

	return 0
}

type wasiSubscription struct {
	userdata uint64
	kind     byte
	fd       uint32
	deadline time.Time
}

// poll treats backend regular files as immediately ready, like native disk
// files. Clock subscriptions still drive os.sleep and the QuickJS timers.
func (c *wasiFSCall) poll() uint32 {
	a := c.args
	if a[2] == 0 {
		return wasiInval
	}

	in, out, result := c.bytes(a[0], a[2]*48), c.bytes(a[1], a[2]*32), c.bytes(a[3], 4)
	if in == nil || out == nil || result == nil {
		return wasiFault
	}

	// Most polls wait on a single timer, so avoid allocating for them.
	var buf [4]wasiSubscription

	subs := buf[:min(a[2], uint64(len(buf)))]
	if a[2] > uint64(len(buf)) {
		subs = make([]wasiSubscription, a[2])
	}

	now := time.Now()

	var earliest time.Time

	for i := range subs {
		b := in[i*48:]
		sub := &subs[i]

		sub.userdata, sub.kind = binary.LittleEndian.Uint64(b), b[8]
		switch sub.kind {
		case 0:
			clock := binary.LittleEndian.Uint32(b[16:])
			timeout := binary.LittleEndian.Uint64(b[24:])
			flags := binary.LittleEndian.Uint16(b[40:])

			clockNow, errno := c.fs.clock(uint64(clock))
			if errno != 0 || flags&^uint16(1) != 0 {
				return wasiInval
			}

			if flags&1 != 0 {
				if timeout < clockNow {
					timeout = 0
				} else {
					timeout -= clockNow
				}
			}
			// Saturate at time.Duration's maximum rather than wrap negative.
			sub.deadline = now.Add(time.Duration(min(timeout, uint64(1<<63-1))))
		case 1, 2:
			sub.fd = binary.LittleEndian.Uint32(b[16:])
			sub.deadline = now
		default:
			return wasiInval
		}

		if earliest.IsZero() || sub.deadline.Before(earliest) {
			earliest = sub.deadline
		}
	}

	if delay := time.Until(earliest); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-c.done:
			c.interrupted = true

			return wasiIntr
		}
	}

	now = time.Now()

	var count uint32

	for _, sub := range subs {
		if sub.deadline.After(now) {
			continue
		}

		b := out[count*32 : (count+1)*32]
		clear(b)
		binary.LittleEndian.PutUint64(b, sub.userdata)

		b[10] = sub.kind
		if sub.kind != 0 {
			n, errno := c.pollFD(sub.fd, sub.kind == 2)
			binary.LittleEndian.PutUint16(b[8:], uint16(errno))
			binary.LittleEndian.PutUint64(b[16:], n)
		}

		count++
	}

	binary.LittleEndian.PutUint32(result, count)

	return 0
}

func (c *wasiFSCall) pollFD(fd uint32, write bool) (uint64, uint32) {
	if fd < 3 {
		if c.fs.stdClosed[fd] || write != (fd != 0) {
			return 0, wasiBadf
		}

		return 0, 0
	}

	f := c.fs.files[fd]
	if f == nil {
		return 0, wasiBadf
	}

	if f.rights&wasiRight(write) == 0 {
		return 0, wasiNotcapable
	}

	info, err := f.file.Stat()
	if err != nil {
		return 0, wasiErrno(err)
	}

	if !info.Mode().IsRegular() {
		return 0, wasiNotsup
	}

	if write {
		return 0, 0
	}

	pos, err := f.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, wasiErrno(err)
	}

	return uint64(max(info.Size()-pos, 0)), 0
}
