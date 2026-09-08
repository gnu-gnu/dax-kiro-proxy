package childproc

import "golang.org/x/sys/unix"

const getTerminalState = unix.TIOCGETA
const setTerminalState = unix.TIOCSETA
