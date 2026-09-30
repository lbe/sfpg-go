#include "textflag.h"

TEXT libc_fstatat64_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_fstatat64(SB)

GLOBL	·libcFstatatTrampolineAddr(SB), RODATA, $8
DATA	·libcFstatatTrampolineAddr(SB)/8, $libc_fstatat64_trampoline<>(SB)
