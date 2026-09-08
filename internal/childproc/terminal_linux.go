package childproc

import "golang.org/x/sys/unix"

const getTerminalState = unix.TCGETS
const setTerminalState = unix.TCSETS
