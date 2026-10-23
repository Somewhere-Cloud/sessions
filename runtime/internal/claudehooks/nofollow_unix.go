//go:build !windows

package claudehooks

import "syscall"

const noFollow = syscall.O_NOFOLLOW
