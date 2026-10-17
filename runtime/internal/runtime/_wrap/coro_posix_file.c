#if defined(__linux__) && !defined(_FILE_OFFSET_BITS)
#define _FILE_OFFSET_BITS 64
#endif

#include <stddef.h>
#include <stdint.h>

#if defined(__APPLE__) || defined(__linux__)
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>

uintptr_t __llgo_runtime_posix_open_into_v1(intptr_t *output, const char *path,
                                            int32_t flags, uint32_t perm) {
  if (output == NULL) {
    return EINVAL;
  }
  int fd = open(path, (int)flags, (mode_t)perm);
  int failure = fd < 0 ? errno : 0;
  *output = (intptr_t)fd;
  return (uintptr_t)failure;
}

uintptr_t __llgo_runtime_posix_fstat_into_v1(int32_t fd, void *output) {
  if (output == NULL) {
    return EINVAL;
  }
  return fstat((int)fd, (struct stat *)output) < 0 ? (uintptr_t)errno : 0;
}
#endif
