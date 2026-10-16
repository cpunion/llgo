//go:build !wasip1 && !js && (wasm || tinygo.wasm)

package syscall

import "unsafe"

// Named freestanding WebAssembly files are complete host operations rather
// than Linux traps. The ordinary synchronous Go signatures remain unchanged;
// llgo.coroHostOperation is the single compiler-owned stack cut, and its
// pointer-shaped operands keep string/slice storage live until exact host
// completion.
func Open(path string, mode int, perm uint32) (fd int, err error) {
	pointer := unsafe.StringData(path)
	r1, _, errno := coroHostOperation4(
		coroHostFileOpenV1,
		unsafe.Pointer(pointer),
		uintptr(len(path)),
		uintptr(mode),
		uintptr(perm),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return -1, operationErr
	}
	return int(r1), nil
}

func Read(fd int, p []byte) (n int, err error) {
	r1, _, errno := coroHostOperation3(
		coroHostFileReadV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

func Write(fd int, p []byte) (n int, err error) {
	r1, _, errno := coroHostOperation3(
		coroHostFileWriteV1,
		uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)),
		uintptr(len(p)),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

// Positional transfers are indivisible host operations, not Seek plus Read or
// Write: concurrent users must not observe a changed descriptor cursor. Keep
// the buffer pointer typed across suspension, and preserve all 64 offset bits
// on wasm32 without depending on the host's native syscall layout.
func Pread(fd int, p []byte, offset int64) (n int, err error) {
	if offset < 0 {
		return 0, EINVAL
	}
	word := uint64(offset)
	r1, _, errno := coroHostOperationPositioned(coroHostFilePreadV1, uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)), uintptr(len(p)),
		uintptr(uint32(word)), uintptr(uint32(word>>32)))
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

func Pwrite(fd int, p []byte, offset int64) (n int, err error) {
	if offset < 0 {
		return 0, EINVAL
	}
	word := uint64(offset)
	r1, _, errno := coroHostOperationPositioned(coroHostFilePwriteV1, uintptr(fd),
		unsafe.Pointer(unsafe.SliceData(p)), uintptr(len(p)),
		uintptr(uint32(word)), uintptr(uint32(word>>32)))
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int(r1), nil
}

func Close(fd int) error {
	r1, _, errno := coroHostOperation1(coroHostFileCloseV1, uintptr(fd))
	return hostOperationError(r1, errno)
}

func Seek(fd int, offset int64, whence int) (newoffset int64, err error) {
	word := uint64(offset)
	r1, r2, errno := coroHostOperationSeek(
		coroHostFileSeekV1,
		uintptr(fd),
		uintptr(uint32(word)),
		uintptr(uint32(word>>32)),
		uintptr(whence),
	)
	if operationErr := hostOperationError(r1, errno); operationErr != nil {
		return 0, operationErr
	}
	return int64(uint64(uint32(r1)) | uint64(uint32(r2))<<32), nil
}

func Unlink(path string) error {
	pointer := unsafe.StringData(path)
	r1, _, errno := coroHostOperation2(
		coroHostFileUnlinkV1,
		unsafe.Pointer(pointer),
		uintptr(len(path)),
	)
	return hostOperationError(r1, errno)
}
