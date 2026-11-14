/private/tmp/llgo-issue-audit-results-20261113/new-performance-followups/darwin-symbol-final-native/bin/llgo-entry-base:	file format mach-o arm64

Disassembly of section __TEXT,__text:

000000010002e69c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop>:
10002e69c:     	stp	x28, x27, [sp, #-0x60]!
10002e6a0:     	stp	x26, x25, [sp, #0x10]
10002e6a4:     	stp	x24, x23, [sp, #0x20]
10002e6a8:     	stp	x22, x21, [sp, #0x30]
10002e6ac:     	stp	x20, x19, [sp, #0x40]
10002e6b0:     	stp	x29, x30, [sp, #0x50]
10002e6b4:     	add	x29, sp, #0x50
10002e6b8:     	sub	sp, sp, #0x1d0
10002e6bc:     	ldr	x8, [x0, #0x48]
10002e6c0:     	cmp	x8, #0x1
10002e6c4:     	b.lt	0x10002feb8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x181c>
10002e6c8:     	ldrb	w15, [x0, #0x20]
10002e6cc:     	mov	x20, x0
10002e6d0:     	mov	x19, x1
10002e6d4:     	str	w15, [sp, #0x24]
10002e6d8:     	ldr	x9, [x20, #0x40]
10002e6dc:     	add	x10, x9, x8, lsl #6
10002e6e0:     	ldur	x9, [x10, #-0x40]
10002e6e4:     	sturb	wzr, [x10, #-0x30]
10002e6e8:     	ldur	x8, [x10, #-0x28]
10002e6ec:     	ldr	x2, [x20, #0x88]
10002e6f0:     	ldur	w0, [x10, #-0x38]
10002e6f4:     	stp	x9, x10, [sp, #0x8]
10002e6f8:     	ldp	x9, x16, [x9, #0x40]
10002e6fc:     	ldr	x10, [x20, #0x78]
10002e700:     	ldr	x21, [x16, #0x98]
10002e704:     	ldp	x23, x22, [x16, #0xb0]
10002e708:     	str	x9, [sp, #0x18]
10002e70c:     	add	x9, x21, x8
10002e710:     	cmp	x9, x2
10002e714:     	b.hi	0x10002fe38 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x179c>
10002e718:     	cmp	x8, x9
10002e71c:     	b.hi	0x10002fe48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17ac>
10002e720:     	sub	x9, x2, x8
10002e724:     	lsl	x8, x8, #4
10002e728:     	cmp	x9, #0x0
10002e72c:     	csel	x8, x8, xzr, gt
10002e730:     	cmp	x22, x0
10002e734:     	b.ls	0x10002fdf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1758>
10002e738:     	add	x28, x10, x8
10002e73c:     	mov	w24, w0
10002e740:     	str	x16, [sp, #0x28]
10002e744:     	b	0x10002e75c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc0>
10002e748:     	add	w26, w24, #0x1
10002e74c:     	mov	w0, w26
10002e750:     	mov	w24, w26
10002e754:     	cmp	x22, x0
10002e758:     	b.ls	0x10002fdf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1758>
10002e75c:     	add	x27, x23, x0, lsl #5
10002e760:     	tbz	w15, #0x0, 0x10002e7a0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x104>
10002e764:     	ldr	x1, [x16, #0x80]
10002e768:     	ldr	w0, [x27, #0x4]
10002e76c:     	cmp	x1, x0
10002e770:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002e774:     	mov	w9, #0xe0               ; =224
10002e778:     	ldr	x8, [x16, #0x78]
10002e77c:     	umaddl	x25, w0, w9, x8
10002e780:     	ldr	w8, [x25]
10002e784:     	cmp	w8, #0x5
10002e788:     	ccmp	w8, #0xb, #0x4, ne
10002e78c:     	ldr	x8, [x20, #0x150]
10002e790:     	cset	w9, ne
10002e794:     	subs	x8, x8, x9
10002e798:     	b.lo	0x10002fc7c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15e0>
10002e79c:     	str	x8, [x20, #0x150]
10002e7a0:     	add	x19, x19, #0x1
10002e7a4:     	tst	x19, #0x3f
10002e7a8:     	b.ne	0x10002e80c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170>
10002e7ac:     	ldr	x8, [x20, #0x160]
10002e7b0:     	cbz	x8, 0x10002e7c0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x124>
10002e7b4:     	cbz	x20, 0x10002e890 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1f4>
10002e7b8:     	ldar	w8, [x8]
10002e7bc:     	cbz	w8, 0x10002ecbc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x620>
10002e7c0:     	ldp	x8, x0, [x20, #0x8]
10002e7c4:     	cbz	x8, 0x10002fea0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1804>
10002e7c8:     	ldr	x9, [x8, #0x8]
10002e7cc:     	ldrb	w10, [x9, #0x17]
10002e7d0:     	tbz	w10, #0x5, 0x10002e7ec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x150>
10002e7d4:     	ldrb	w9, [x9, #0x17]
10002e7d8:     	and	w9, w9, #0x1f
10002e7dc:     	cmp	w9, #0x16
10002e7e0:     	b.eq	0x10002e7ec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x150>
10002e7e4:     	stur	x0, [x29, #-0x78]
10002e7e8:     	sub	x0, x29, #0x78
10002e7ec:     	ldr	x8, [x8, #0x28]
10002e7f0:     	blr	x8
10002e7f4:     	ldr	w15, [sp, #0x24]
10002e7f8:     	ldr	x16, [sp, #0x28]
10002e7fc:     	mov	x2, x1
10002e800:     	cbz	x0, 0x10002e8a4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x208>
10002e804:     	ldr	x8, [x0, #0x8]
10002e808:     	cbnz	x8, 0x10002fcc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1628>
10002e80c:     	ldr	w10, [x27, #0x1c]
10002e810:     	cbz	w10, 0x10002e8ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x210>
10002e814:     	tbz	w15, #0x0, 0x10002e830 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x194>
10002e818:     	ldr	w9, [x27]
10002e81c:     	cmp	w9, #0x20
10002e820:     	b.eq	0x10002e8ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x210>
10002e824:     	cmp	w9, #0x41
10002e828:     	b.eq	0x10002e8ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x210>
10002e82c:     	b	0x10002e8b0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x214>
10002e830:     	ldr	w9, [x27]
10002e834:     	cmp	w9, #0x20
10002e838:     	b.eq	0x10002e844 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a8>
10002e83c:     	cmp	w9, #0x41
10002e840:     	b.ne	0x10002e8b0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x214>
10002e844:     	mvn	w8, w19
10002e848:     	tst	x8, #0x3f
10002e84c:     	b.eq	0x10002e8ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x210>
10002e850:     	cmp	w9, #0x41
10002e854:     	b.ne	0x10002e870 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1d4>
10002e858:     	ldr	w0, [x27, #0x8]
10002e85c:     	cmp	x21, x0
10002e860:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002e864:     	ldr	w8, [x27, #0x18]
10002e868:     	add	x9, x28, x0, lsl #4
10002e86c:     	stp	x8, xzr, [x9]
10002e870:     	add	w24, w24, #0x1
10002e874:     	cmp	x22, x24
10002e878:     	b.ls	0x10002fddc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1740>
10002e87c:     	add	x27, x23, x24, lsl #5
10002e880:     	add	x19, x19, #0x1
10002e884:     	ldr	w10, [x27, #0x1c]
10002e888:     	cbnz	w10, 0x10002e830 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x194>
10002e88c:     	b	0x10002e8ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x210>
10002e890:     	mov	w0, #0x1                ; =1
10002e894:     	bl	0x10020c0b8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002e898:     	ldr	x16, [sp, #0x28]
10002e89c:     	ldr	w15, [sp, #0x24]
10002e8a0:     	cbnz	x0, 0x10002e804 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x168>
10002e8a4:     	ldr	w10, [x27, #0x1c]
10002e8a8:     	cbnz	w10, 0x10002e814 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x178>
10002e8ac:     	ldr	w9, [x27]
10002e8b0:     	mov	w11, #0xff20            ; =65312
10002e8b4:     	sub	w8, w9, #0xe0
10002e8b8:     	movk	w11, #0xff, lsl #16
10002e8bc:     	cmp	w8, w11
10002e8c0:     	b.hs	0x10002eb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x468>
10002e8c4:     	lsr	w8, w9, #16
10002e8c8:     	cmp	w8, #0xfb
10002e8cc:     	b.eq	0x10002ea54 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x3b8>
10002e8d0:     	cmp	w8, #0xfc
10002e8d4:     	b.eq	0x10002e950 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x2b4>
10002e8d8:     	cmp	w8, #0xfd
10002e8dc:     	b.ne	0x10002eafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x460>
10002e8e0:     	ldr	w0, [x27, #0xc]
10002e8e4:     	cmn	w0, #0x1
10002e8e8:     	b.eq	0x10002ed44 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6a8>
10002e8ec:     	cmp	x21, x0
10002e8f0:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002e8f4:     	add	x8, x28, x0, lsl #4
10002e8f8:     	ldp	x2, x8, [x8]
10002e8fc:     	ldr	w0, [x27, #0x10]
10002e900:     	cmn	w0, #0x1
10002e904:     	b.eq	0x10002ed58 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6bc>
10002e908:     	cmp	x21, x0
10002e90c:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002e910:     	ldr	q0, [x28, x0, lsl #4]
10002e914:     	stur	q0, [x29, #-0xa0]
10002e918:     	ldp	x5, x6, [x29, #-0xa0]
10002e91c:     	ldr	w0, [x27, #0x14]
10002e920:     	cmn	w0, #0x1
10002e924:     	b.eq	0x10002ed6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6d0>
10002e928:     	cmp	x21, x0
10002e92c:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002e930:     	ldr	q0, [x28, x0, lsl #4]
10002e934:     	stur	q0, [x29, #-0x90]
10002e938:     	ldp	x10, x11, [x29, #-0x90]
10002e93c:     	ldr	x1, [x16, #0x80]
10002e940:     	ldr	w0, [x27, #0x4]
10002e944:     	cmp	x1, x0
10002e948:     	b.hi	0x10002ed84 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6e8>
10002e94c:     	b	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002e950:     	mov	w8, #0xffed             ; =65517
10002e954:     	movk	w8, #0xff03, lsl #16
10002e958:     	add	w8, w9, w8
10002e95c:     	cmp	w8, #0x4
10002e960:     	b.hs	0x10002eb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x468>
10002e964:     	ldr	w0, [x27, #0x18]
10002e968:     	ldr	x8, [x16, #0xd8]
10002e96c:     	add	w10, w10, #0x2
10002e970:     	ldr	x9, [x16, #0xc8]
10002e974:     	add	w2, w10, w0
10002e978:     	cmp	x8, x2
10002e97c:     	b.lo	0x10002fe24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1788>
10002e980:     	cmp	w0, w2
10002e984:     	b.hi	0x10002fe94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17f8>
10002e988:     	sub	x8, x8, x0
10002e98c:     	lsl	x11, x0, #2
10002e990:     	cmp	x8, #0x0
10002e994:     	csel	x8, x11, xzr, gt
10002e998:     	cbz	w10, 0x10002fe70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17d4>
10002e99c:     	add	x10, x9, x8
10002e9a0:     	ldr	w8, [x10]
10002e9a4:     	cmp	x21, x8
10002e9a8:     	b.ls	0x10002fe18 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x177c>
10002e9ac:     	sub	x1, x2, x0
10002e9b0:     	cmp	x1, #0x1
10002e9b4:     	b.eq	0x10002fe68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17cc>
10002e9b8:     	ldr	w0, [x10, #0x4]
10002e9bc:     	cmp	x21, x0
10002e9c0:     	b.ls	0x10002fe10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1774>
10002e9c4:     	ldr	w14, [x27]
10002e9c8:     	lsl	x8, x8, #4
10002e9cc:     	lsl	x9, x0, #4
10002e9d0:     	mov	w11, #0xffed            ; =65517
10002e9d4:     	movk	w11, #0xff03, lsl #16
10002e9d8:     	ldr	x13, [x28, x8]
10002e9dc:     	ldr	x9, [x28, x9]
10002e9e0:     	add	w11, w14, w11
10002e9e4:     	cmp	w11, #0x2
10002e9e8:     	b.hs	0x10002ee64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7c8>
10002e9ec:     	cmp	x1, #0x3
10002e9f0:     	b.lo	0x10002fe84 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e8>
10002e9f4:     	ldr	w0, [x10, #0x8]
10002e9f8:     	cmp	x21, x0
10002e9fc:     	b.ls	0x10002fe10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1774>
10002ea00:     	cmp	x1, #0x3
10002ea04:     	b.eq	0x10002fe7c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e0>
10002ea08:     	ldr	w8, [x10, #0xc]
10002ea0c:     	cmp	x21, x8
10002ea10:     	b.ls	0x10002fe18 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x177c>
10002ea14:     	lsl	x11, x0, #4
10002ea18:     	lsl	x8, x8, #4
10002ea1c:     	ldr	x12, [x28, x11]
10002ea20:     	mov	w11, #0x13              ; =19
10002ea24:     	ldr	x8, [x28, x8]
10002ea28:     	movk	w11, #0xfc, lsl #16
10002ea2c:     	cmp	w14, w11
10002ea30:     	b.ne	0x10002f044 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x9a8>
10002ea34:     	add	x11, x12, x13
10002ea38:     	orr	x14, x12, x13
10002ea3c:     	and	x12, x12, x13
10002ea40:     	bic	x13, x14, x11
10002ea44:     	orr	x12, x13, x12
10002ea48:     	add	x9, x9, x12, lsr #63
10002ea4c:     	add	x12, x9, x8
10002ea50:     	b	0x10002f060 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x9c4>
10002ea54:     	sub	w8, w9, #0x2
10002ea58:     	and	w9, w8, #0xffff
10002ea5c:     	cmp	w9, #0x21
10002ea60:     	mov	x9, #0x3e0f             ; =15887
10002ea64:     	movk	x9, #0x1c00, lsl #16
10002ea68:     	cset	w10, lo
10002ea6c:     	movk	x9, #0x1, lsl #32
10002ea70:     	lsr	x9, x9, x8
10002ea74:     	and	w9, w10, w9
10002ea78:     	tbz	w9, #0x0, 0x10002ec98 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5fc>
10002ea7c:     	ldr	w0, [x27, #0xc]
10002ea80:     	cmn	w0, #0x1
10002ea84:     	b.eq	0x10002ef10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x874>
10002ea88:     	cmp	x21, x0
10002ea8c:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002ea90:     	ldr	q0, [x28, x0, lsl #4]
10002ea94:     	stur	q0, [x29, #-0xd0]
10002ea98:     	ldp	x3, x4, [x29, #-0xd0]
10002ea9c:     	ldr	w0, [x27, #0x10]
10002eaa0:     	cmn	w0, #0x1
10002eaa4:     	b.eq	0x10002ef24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x888>
10002eaa8:     	cmp	x21, x0
10002eaac:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002eab0:     	ldr	q0, [x28, x0, lsl #4]
10002eab4:     	stur	q0, [x29, #-0xc0]
10002eab8:     	ldp	x5, x6, [x29, #-0xc0]
10002eabc:     	and	w8, w8, #0xffff
10002eac0:     	cmp	w8, #0xc
10002eac4:     	b.ne	0x10002ef38 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x89c>
10002eac8:     	ldr	w8, [x27, #0x18]
10002eacc:     	ldr	x1, [x16, #0xd0]
10002ead0:     	add	w0, w8, #0x2
10002ead4:     	cmp	x1, x0
10002ead8:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002eadc:     	ldr	x8, [x16, #0xc8]
10002eae0:     	ldr	w0, [x8, x0, lsl #2]
10002eae4:     	cmp	x21, x0
10002eae8:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002eaec:     	ldr	q0, [x28, x0, lsl #4]
10002eaf0:     	stur	q0, [x29, #-0xb0]
10002eaf4:     	ldp	x7, x9, [x29, #-0xb0]
10002eaf8:     	b	0x10002ef40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8a4>
10002eafc:     	cmp	w9, #0xe7
10002eb00:     	b.lo	0x10002fbf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eb04:     	ldr	w0, [x27, #0xc]
10002eb08:     	cmn	w0, #0x1
10002eb0c:     	b.eq	0x10002eb90 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4f4>
10002eb10:     	cmp	x21, x0
10002eb14:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002eb18:     	add	x8, x28, x0, lsl #4
10002eb1c:     	ldp	x5, x4, [x8]
10002eb20:     	ldr	w0, [x27, #0x10]
10002eb24:     	cmn	w0, #0x1
10002eb28:     	b.eq	0x10002eba4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x508>
10002eb2c:     	cmp	x21, x0
10002eb30:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002eb34:     	add	x8, x28, x0, lsl #4
10002eb38:     	ldp	x7, x6, [x8]
10002eb3c:     	sub	w8, w9, #0x45
10002eb40:     	cmp	w8, #0x7f
10002eb44:     	b.hi	0x10002ebb8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x51c>
10002eb48:     	b.hi	0x10002f7bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1120>
10002eb4c:     	adrp	x12, 0x100223000 <dyld_stub_binder+0x100223000>
10002eb50:     	add	x12, x12, #0x340
10002eb54:     	adr	x10, 0x10002eb54 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b8>
10002eb58:     	ldrsw	x11, [x12, x8, lsl #2]
10002eb5c:     	add	x10, x10, x11
10002eb60:     	br	x10
10002eb64:     	mov	w0, w9
10002eb68:     	mov	x1, x5
10002eb6c:     	mov	x2, x7
10002eb70:     	bl	0x100054998 <_github.com/cpunion/wasmvm/internal/engine/numeric.Eval>
10002eb74:     	ldr	x16, [sp, #0x28]
10002eb78:     	ldr	w15, [sp, #0x24]
10002eb7c:     	mov	x25, x1
10002eb80:     	tst	w25, #0xff
10002eb84:     	b.ne	0x10002fd40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16a4>
10002eb88:     	mov	x1, xzr
10002eb8c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002eb90:     	mov	x5, xzr
10002eb94:     	mov	x4, xzr
10002eb98:     	ldr	w0, [x27, #0x10]
10002eb9c:     	cmn	w0, #0x1
10002eba0:     	b.ne	0x10002eb2c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x490>
10002eba4:     	mov	x7, xzr
10002eba8:     	mov	x6, xzr
10002ebac:     	sub	w8, w9, #0x45
10002ebb0:     	cmp	w8, #0x7f
10002ebb4:     	b.ls	0x10002eb48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4ac>
10002ebb8:     	sub	w8, w9, #0x1
10002ebbc:     	cmp	w8, #0xd3
10002ebc0:     	b.hi	0x10002ec68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5cc>
10002ebc4:     	adrp	x13, 0x100222000 <dyld_stub_binder+0x100222000>
10002ebc8:     	add	x13, x13, #0xff0
10002ebcc:     	mov	x0, xzr
10002ebd0:     	adr	x11, 0x10002ebd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x534>
10002ebd4:     	ldrsw	x12, [x13, x8, lsl #2]
10002ebd8:     	add	x11, x11, x12
10002ebdc:     	mov	x1, xzr
10002ebe0:     	br	x11
10002ebe4:     	ldr	x1, [x16, #0x80]
10002ebe8:     	ldr	w0, [x27, #0x4]
10002ebec:     	cmp	x1, x0
10002ebf0:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002ebf4:     	ldr	x8, [x16, #0x78]
10002ebf8:     	mov	w10, #0xe0              ; =224
10002ebfc:     	umaddl	x8, w0, w10, x8
10002ec00:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ec04:     	ldr	x10, [sp, #0x18]
10002ec08:     	ldr	w0, [x8, #0x18]
10002ec0c:     	ldr	x1, [x10, #0x78]
10002ec10:     	cmp	x1, x0
10002ec14:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002ec18:     	ldr	x10, [x10, #0x70]
10002ec1c:     	adds	x10, x10, x0, lsl #3
10002ec20:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ec24:     	ldr	x10, [x10]
10002ec28:     	cbz	x10, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ec2c:     	ldr	x0, [x10, #0x10]
10002ec30:     	ldr	w10, [x8, #0x14]
10002ec34:     	ldr	w8, [x8, #0x1c]
10002ec38:     	ldr	x11, [x0, #0x40]
10002ec3c:     	orr	x3, x10, x8, lsl #32
10002ec40:     	cbz	x11, 0x10002ec4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5b0>
10002ec44:     	ldr	x8, [x0, #0x60]
10002ec48:     	cbz	x8, 0x10002efec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ec4c:     	sub	x8, x29, #0x78
10002ec50:     	mov	w1, w9
10002ec54:     	mov	x2, x5
10002ec58:     	mov	w4, wzr
10002ec5c:     	mov	x5, xzr
10002ec60:     	mov	x6, xzr
10002ec64:     	b	0x10002ee0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x770>
10002ec68:     	sub	w8, w9, #0xfc0, lsl #12 ; =0xfc0000
10002ec6c:     	cmp	w8, #0x8
10002ec70:     	b.hs	0x10002ee18 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x77c>
10002ec74:     	mov	w0, w9
10002ec78:     	mov	x1, x5
10002ec7c:     	mov	x2, x7
10002ec80:     	bl	0x100054998 <_github.com/cpunion/wasmvm/internal/engine/numeric.Eval>
10002ec84:     	ldr	x16, [sp, #0x28]
10002ec88:     	ldr	w15, [sp, #0x24]
10002ec8c:     	mov	x25, x1
10002ec90:     	mov	x1, xzr
10002ec94:     	b	0x10002f01c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x980>
10002ec98:     	ldr	x8, [sp, #0x10]
10002ec9c:     	mov	x0, x20
10002eca0:     	stur	w24, [x8, #-0x38]
10002eca4:     	bl	0x1000300d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runGC>
10002eca8:     	ldr	x16, [sp, #0x28]
10002ecac:     	ldr	w15, [sp, #0x24]
10002ecb0:     	tst	w0, #0xff
10002ecb4:     	b.eq	0x10002efc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x928>
10002ecb8:     	b	0x10002fcd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1634>
10002ecbc:     	mov	x0, xzr
10002ecc0:     	mov	x2, xzr
10002ecc4:     	b	0x10002e8a4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x208>
10002ecc8:     	ldr	x1, [x16, #0x80]
10002eccc:     	ldr	w0, [x27, #0x4]
10002ecd0:     	cmp	x1, x0
10002ecd4:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002ecd8:     	ldr	x8, [x16, #0x78]
10002ecdc:     	mov	w10, #0xe0              ; =224
10002ece0:     	umaddl	x8, w0, w10, x8
10002ece4:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ece8:     	ldr	x10, [sp, #0x18]
10002ecec:     	ldr	w0, [x8, #0x18]
10002ecf0:     	ldr	x1, [x10, #0x78]
10002ecf4:     	cmp	x1, x0
10002ecf8:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002ecfc:     	ldr	x10, [x10, #0x70]
10002ed00:     	adds	x10, x10, x0, lsl #3
10002ed04:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ed08:     	ldr	x10, [x10]
10002ed0c:     	cbz	x10, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ed10:     	ldr	x0, [x10, #0x10]
10002ed14:     	ldr	w10, [x8, #0x14]
10002ed18:     	ldr	w8, [x8, #0x1c]
10002ed1c:     	ldr	x11, [x0, #0x40]
10002ed20:     	orr	x3, x10, x8, lsl #32
10002ed24:     	cbz	x11, 0x10002ed30 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x694>
10002ed28:     	ldr	x8, [x0, #0x60]
10002ed2c:     	cbz	x8, 0x10002f0fc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa60>
10002ed30:     	mov	w1, w9
10002ed34:     	mov	x2, x5
10002ed38:     	mov	w4, wzr
10002ed3c:     	mov	x5, x7
10002ed40:     	b	0x10002eef8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x85c>
10002ed44:     	mov	x8, xzr
10002ed48:     	mov	x2, xzr
10002ed4c:     	ldr	w0, [x27, #0x10]
10002ed50:     	cmn	w0, #0x1
10002ed54:     	b.ne	0x10002e908 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x26c>
10002ed58:     	mov	x5, xzr
10002ed5c:     	mov	x6, xzr
10002ed60:     	ldr	w0, [x27, #0x14]
10002ed64:     	cmn	w0, #0x1
10002ed68:     	b.ne	0x10002e928 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x28c>
10002ed6c:     	mov	x10, xzr
10002ed70:     	mov	x11, xzr
10002ed74:     	ldr	x1, [x16, #0x80]
10002ed78:     	ldr	w0, [x27, #0x4]
10002ed7c:     	cmp	x1, x0
10002ed80:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002ed84:     	mov	w13, #0xe0              ; =224
10002ed88:     	ldr	x12, [x16, #0x78]
10002ed8c:     	umaddl	x0, w0, w13, x12
10002ed90:     	cbz	x0, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ed94:     	and	w12, w9, #0xfffc
10002ed98:     	and	w13, w9, #0xffff
10002ed9c:     	cmp	w12, #0x54
10002eda0:     	ccmp	w13, #0xb, #0x0, ne
10002eda4:     	b.lo	0x10002edb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x718>
10002eda8:     	sub	w14, w13, #0x5c
10002edac:     	cmp	w14, #0x2
10002edb0:     	b.hs	0x10002ee94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7f8>
10002edb4:     	ldr	x10, [sp, #0x18]
10002edb8:     	ldr	w8, [x0, #0x18]
10002edbc:     	ldr	x1, [x10, #0x78]
10002edc0:     	cmp	x1, x8
10002edc4:     	b.ls	0x10002fe08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x176c>
10002edc8:     	ldr	x10, [x10, #0x70]
10002edcc:     	adds	x8, x10, x8, lsl #3
10002edd0:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002edd4:     	ldr	x8, [x8]
10002edd8:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002eddc:     	ldr	x10, [x8, #0x10]
10002ede0:     	ldr	w8, [x0, #0x14]
10002ede4:     	ldr	w11, [x0, #0x1c]
10002ede8:     	ldrb	w4, [x0, #0x40]
10002edec:     	ldr	x12, [x10, #0x40]
10002edf0:     	orr	x3, x8, x11, lsl #32
10002edf4:     	cbz	x12, 0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x764>
10002edf8:     	ldr	x8, [x10, #0x60]
10002edfc:     	cbz	x8, 0x10002efcc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x930>
10002ee00:     	sub	x8, x29, #0x78
10002ee04:     	mov	x0, x10
10002ee08:     	mov	w1, w9
10002ee0c:     	sub	x26, x29, #0x78
10002ee10:     	bl	0x10004cbb0 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).load>
10002ee14:     	b	0x10002f00c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x970>
10002ee18:     	mov	w8, #-0x1000001         ; =-16777217
10002ee1c:     	add	w8, w9, w8
10002ee20:     	cmp	w8, #0x8
10002ee24:     	b.hi	0x10002f96c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x12d0>
10002ee28:     	mov	w11, #0x117             ; =279
10002ee2c:     	lsr	w11, w11, w8
10002ee30:     	tbz	w11, #0x0, 0x10002f95c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x12c0>
10002ee34:     	and	w9, w9, #0xff
10002ee38:     	mov	w8, wzr
10002ee3c:     	cmp	w9, #0x4
10002ee40:     	b.gt	0x10002f110 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa74>
10002ee44:     	cmp	w9, #0x2
10002ee48:     	b.eq	0x10002f14c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xab0>
10002ee4c:     	cmp	w9, #0x3
10002ee50:     	b.ne	0x10002f834 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1198>
10002ee54:     	sub	w8, w10, #0x1
10002ee58:     	cmp	w8, w5
10002ee5c:     	csel	w8, w8, w5, lo
10002ee60:     	b	0x10002f834 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1198>
10002ee64:     	umulh	x12, x9, x13
10002ee68:     	mov	w8, #0x13               ; =19
10002ee6c:     	movk	w8, #0xfc, lsl #16
10002ee70:     	mul	x11, x9, x13
10002ee74:     	add	w8, w8, #0x2
10002ee78:     	cmp	w14, w8
10002ee7c:     	b.ne	0x10002f060 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x9c4>
10002ee80:     	and	x8, x9, x13, asr #63
10002ee84:     	and	x9, x13, x9, asr #63
10002ee88:     	add	x8, x9, x8
10002ee8c:     	sub	x12, x12, x8
10002ee90:     	b	0x10002f060 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x9c4>
10002ee94:     	cmp	w13, #0xb
10002ee98:     	b.eq	0x10002eea4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x808>
10002ee9c:     	cmp	w12, #0x58
10002eea0:     	b.ne	0x10002f124 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa88>
10002eea4:     	ldr	x10, [sp, #0x18]
10002eea8:     	ldr	w8, [x0, #0x18]
10002eeac:     	ldr	x1, [x10, #0x78]
10002eeb0:     	cmp	x1, x8
10002eeb4:     	b.ls	0x10002fe08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x176c>
10002eeb8:     	ldr	x10, [x10, #0x70]
10002eebc:     	adds	x8, x10, x8, lsl #3
10002eec0:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002eec4:     	ldr	x8, [x8]
10002eec8:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002eecc:     	ldr	x8, [x8, #0x10]
10002eed0:     	ldr	w10, [x0, #0x14]
10002eed4:     	ldr	w11, [x0, #0x1c]
10002eed8:     	ldrb	w4, [x0, #0x40]
10002eedc:     	ldr	x12, [x8, #0x40]
10002eee0:     	orr	x3, x10, x11, lsl #32
10002eee4:     	cbz	x12, 0x10002eef0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x854>
10002eee8:     	ldr	x10, [x8, #0x60]
10002eeec:     	cbz	x10, 0x10002f804 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1168>
10002eef0:     	mov	x0, x8
10002eef4:     	mov	w1, w9
10002eef8:     	bl	0x10004a544 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).store>
10002eefc:     	mov	w8, w0
10002ef00:     	mov	x0, xzr
10002ef04:     	mov	x1, xzr
10002ef08:     	and	w25, w8, #0xff
10002ef0c:     	b	0x10002f014 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x978>
10002ef10:     	mov	x3, xzr
10002ef14:     	mov	x4, xzr
10002ef18:     	ldr	w0, [x27, #0x10]
10002ef1c:     	cmn	w0, #0x1
10002ef20:     	b.ne	0x10002eaa8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x40c>
10002ef24:     	mov	x5, xzr
10002ef28:     	mov	x6, xzr
10002ef2c:     	and	w8, w8, #0xffff
10002ef30:     	cmp	w8, #0xc
10002ef34:     	b.eq	0x10002eac8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x42c>
10002ef38:     	mov	x7, xzr
10002ef3c:     	mov	x9, xzr
10002ef40:     	cbz	x20, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ef44:     	ldr	x1, [x16, #0x80]
10002ef48:     	ldr	w0, [x27, #0x4]
10002ef4c:     	cmp	x1, x0
10002ef50:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002ef54:     	mov	w10, #0xe0              ; =224
10002ef58:     	ldr	x8, [x16, #0x78]
10002ef5c:     	umaddl	x2, w0, w10, x8
10002ef60:     	cbz	x2, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002ef64:     	ldr	x0, [x20]
10002ef68:     	ldr	x1, [sp, #0x18]
10002ef6c:     	add	x8, sp, #0x30
10002ef70:     	str	x9, [sp]
10002ef74:     	bl	0x10003a60c <_github.com/cpunion/wasmvm/internal/engine/state.(*Store).GCAccess>
10002ef78:     	ldp	x25, x26, [sp, #0x40]
10002ef7c:     	ldr	q0, [sp, #0x30]
10002ef80:     	cbz	x25, 0x10002efa4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x908>
10002ef84:     	ldr	x8, [x25, #0x8]
10002ef88:     	ldr	w15, [sp, #0x24]
10002ef8c:     	cbnz	x8, 0x10002fcd8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x163c>
10002ef90:     	ldr	w0, [x27, #0x8]
10002ef94:     	ldr	x16, [sp, #0x28]
10002ef98:     	cmn	w0, #0x1
10002ef9c:     	b.ne	0x10002efb8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x91c>
10002efa0:     	b	0x10002efc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x928>
10002efa4:     	ldr	w15, [sp, #0x24]
10002efa8:     	ldr	w0, [x27, #0x8]
10002efac:     	ldr	x16, [sp, #0x28]
10002efb0:     	cmn	w0, #0x1
10002efb4:     	b.eq	0x10002efc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x928>
10002efb8:     	cmp	x21, x0
10002efbc:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002efc0:     	str	q0, [x28, x0, lsl #4]
10002efc4:     	add	w26, w24, #0x1
10002efc8:     	b	0x10002f0a4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa08>
10002efcc:     	sub	x8, x29, #0x78
10002efd0:     	mov	x0, x10
10002efd4:     	mov	w1, w9
10002efd8:     	b	0x10002f004 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x968>
10002efdc:     	ldr	w8, [x27, #0x18]
10002efe0:     	mov	x1, xzr
10002efe4:     	orr	x0, x8, x10, lsl #32
10002efe8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002efec:     	sub	x8, x29, #0x78
10002eff0:     	mov	w1, w9
10002eff4:     	mov	x2, x5
10002eff8:     	mov	w4, wzr
10002effc:     	mov	x5, xzr
10002f000:     	mov	x6, xzr
10002f004:     	sub	x26, x29, #0x78
10002f008:     	bl	0x10004ccd8 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).loadLocked>
10002f00c:     	ldp	x0, x1, [x26]
10002f010:     	ldrb	w25, [x26, #0x10]
10002f014:     	ldr	w15, [sp, #0x24]
10002f018:     	ldr	x16, [sp, #0x28]
10002f01c:     	tst	w25, #0xff
10002f020:     	b.ne	0x10002fd40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16a4>
10002f024:     	ldr	w8, [x27, #0x8]
10002f028:     	cmn	w8, #0x1
10002f02c:     	b.eq	0x10002e748 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xac>
10002f030:     	cmp	x21, x8
10002f034:     	b.ls	0x10002fdfc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1760>
10002f038:     	add	x8, x28, x8, lsl #4
10002f03c:     	stp	x0, x1, [x8]
10002f040:     	b	0x10002e748 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xac>
10002f044:     	sub	x11, x13, x12
10002f048:     	eon	x14, x13, x12
10002f04c:     	bic	x12, x12, x13
10002f050:     	and	x13, x11, x14
10002f054:     	orr	x12, x13, x12
10002f058:     	add	x9, x9, x12, asr #63
10002f05c:     	sub	x12, x9, x8
10002f060:     	ldr	w0, [x27, #0x1c]
10002f064:     	cmp	x1, x0
10002f068:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f06c:     	add	w8, w0, #0x1
10002f070:     	cmp	x1, x8
10002f074:     	b.ls	0x10002fe08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x176c>
10002f078:     	ldr	w9, [x10, x0, lsl #2]
10002f07c:     	cmp	x21, x9
10002f080:     	b.ls	0x10002fe5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17c0>
10002f084:     	ldr	w0, [x10, x8, lsl #2]
10002f088:     	add	x8, x28, x9, lsl #4
10002f08c:     	cmp	x21, x0
10002f090:     	stp	x11, xzr, [x8]
10002f094:     	b.ls	0x10002fe10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1774>
10002f098:     	add	x8, x28, x0, lsl #4
10002f09c:     	add	w26, w24, #0x1
10002f0a0:     	stp	x12, xzr, [x8]
10002f0a4:     	ldr	x8, [sp, #0x10]
10002f0a8:     	stur	w26, [x8, #-0x38]
10002f0ac:     	b	0x10002e74c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xb0>
10002f0b0:     	ldr	w0, [x27, #0x18]
10002f0b4:     	cmp	x21, x0
10002f0b8:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002f0bc:     	add	x8, x28, x0, lsl #4
10002f0c0:     	mov	x0, x5
10002f0c4:     	mov	x1, x4
10002f0c8:     	stp	x5, x4, [x8]
10002f0cc:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f0d0:     	ldr	w0, [x27, #0x14]
10002f0d4:     	cmn	w0, #0x1
10002f0d8:     	b.eq	0x10002f820 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1184>
10002f0dc:     	cmp	x21, x0
10002f0e0:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002f0e4:     	lsl	x8, x0, #4
10002f0e8:     	ldr	w8, [x28, x8]
10002f0ec:     	cmp	x8, #0x0
10002f0f0:     	csel	x0, x7, x5, eq
10002f0f4:     	csel	x1, x6, x4, eq
10002f0f8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f0fc:     	mov	w1, w9
10002f100:     	mov	x2, x5
10002f104:     	mov	w4, wzr
10002f108:     	mov	x5, x7
10002f10c:     	b	0x10002f80c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1170>
10002f110:     	cmp	w9, #0x9
10002f114:     	b.ne	0x10002f82c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1190>
10002f118:     	cmp	x5, #0x0
10002f11c:     	cset	w8, ne
10002f120:     	b	0x10002f834 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1198>
10002f124:     	mov	x1, x2
10002f128:     	mov	x2, x8
10002f12c:     	mov	x3, x5
10002f130:     	mov	x4, x6
10002f134:     	mov	x5, x10
10002f138:     	mov	x6, x11
10002f13c:     	bl	0x100052e94 <_github.com/cpunion/wasmvm/internal/engine/simd.Eval>
10002f140:     	ldr	x16, [sp, #0x28]
10002f144:     	ldr	w15, [sp, #0x24]
10002f148:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f14c:     	cmp	w5, #0x0
10002f150:     	cset	w8, eq
10002f154:     	b	0x10002f834 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1198>
10002f158:     	cmp	x5, x7
10002f15c:     	mov	x1, xzr
10002f160:     	cset	w0, eq
10002f164:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f168:     	ldr	x8, [sp, #0x18]
10002f16c:     	ldr	w0, [x27, #0x18]
10002f170:     	ldr	x1, [x8, #0x78]
10002f174:     	cmp	x1, x0
10002f178:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f17c:     	ldr	x8, [x8, #0x70]
10002f180:     	adds	x8, x8, x0, lsl #3
10002f184:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f188:     	cbz	x20, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f18c:     	ldr	x25, [x8]
10002f190:     	cbz	x25, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f194:     	ldr	x8, [x25, #0x10]
10002f198:     	ldr	x0, [x20]
10002f19c:     	mov	w9, w5
10002f1a0:     	mov	x1, x25
10002f1a4:     	ldrb	w8, [x8, #0x31]
10002f1a8:     	tst	w8, #0x1
10002f1ac:     	strb	w8, [sp, #0x56]
10002f1b0:     	csel	x2, x5, x9, ne
10002f1b4:     	bl	0x1000470bc <_github.com/cpunion/wasmvm/internal/engine/state.(*Store).GrowMemory>
10002f1b8:     	tbz	w1, #0x0, 0x10002facc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1430>
10002f1bc:     	mov	x1, xzr
10002f1c0:     	b	0x10002fae8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x144c>
10002f1c4:     	ldr	x8, [sp, #0x18]
10002f1c8:     	ldr	w0, [x27, #0x18]
10002f1cc:     	ldr	x1, [x8, #0x60]
10002f1d0:     	cmp	x1, x0
10002f1d4:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f1d8:     	ldr	x8, [x8, #0x58]
10002f1dc:     	adds	x8, x8, x0, lsl #3
10002f1e0:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f1e4:     	ldr	x8, [x8]
10002f1e8:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f1ec:     	ldrb	w9, [x8, #0x29]
10002f1f0:     	mov	w10, w5
10002f1f4:     	ldr	x11, [x8, #0x40]
10002f1f8:     	cmp	w9, #0x0
10002f1fc:     	csel	x9, x5, x10, ne
10002f200:     	cmp	x9, x11
10002f204:     	b.hs	0x10002fd2c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1690>
10002f208:     	ldr	x8, [x8, #0x38]
10002f20c:     	mov	x1, xzr
10002f210:     	ldr	x0, [x8, x9, lsl #3]
10002f214:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f218:     	ldr	x8, [sp, #0x18]
10002f21c:     	ldr	w0, [x27, #0x18]
10002f220:     	ldr	x1, [x8, #0x48]
10002f224:     	cmp	x1, x0
10002f228:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f22c:     	ldr	x8, [x8, #0x40]
10002f230:     	mov	x1, xzr
10002f234:     	ldr	x0, [x8, x0, lsl #3]
10002f238:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f23c:     	mov	x0, x5
10002f240:     	mov	x1, x4
10002f244:     	cbnz	x5, 0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f248:     	b	0x10002fd3c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16a0>
10002f24c:     	ldr	x9, [sp, #0x18]
10002f250:     	ldr	w8, [x27, #0x18]
10002f254:     	ldr	x1, [x9, #0x90]
10002f258:     	cmp	x1, x8
10002f25c:     	b.ls	0x10002fe08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x176c>
10002f260:     	ldr	x9, [x9, #0x88]
10002f264:     	mov	x0, xzr
10002f268:     	mov	x1, xzr
10002f26c:     	ldr	x8, [x9, x8, lsl #3]
10002f270:     	stp	x5, x4, [x8, #0x20]
10002f274:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f278:     	ldr	x8, [sp, #0x18]
10002f27c:     	ldr	w0, [x27, #0x18]
10002f280:     	ldr	x1, [x8, #0x90]
10002f284:     	cmp	x1, x0
10002f288:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f28c:     	ldr	x8, [x8, #0x88]
10002f290:     	ldr	x8, [x8, x0, lsl #3]
10002f294:     	ldp	x0, x1, [x8, #0x20]
10002f298:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f29c:     	ldr	x8, [sp, #0x18]
10002f2a0:     	ldr	w0, [x27, #0x18]
10002f2a4:     	ldr	x1, [x8, #0x60]
10002f2a8:     	cmp	x1, x0
10002f2ac:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f2b0:     	ldr	x8, [x8, #0x58]
10002f2b4:     	adds	x8, x8, x0, lsl #3
10002f2b8:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f2bc:     	ldr	x8, [x8]
10002f2c0:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f2c4:     	ldrb	w9, [x8, #0x29]
10002f2c8:     	mov	w10, w5
10002f2cc:     	ldr	x11, [x8, #0x40]
10002f2d0:     	cmp	w9, #0x0
10002f2d4:     	csel	x9, x5, x10, ne
10002f2d8:     	cmp	x9, x11
10002f2dc:     	b.hs	0x10002fd2c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1690>
10002f2e0:     	ldr	x8, [x8, #0x38]
10002f2e4:     	mov	x0, xzr
10002f2e8:     	mov	x1, xzr
10002f2ec:     	str	x7, [x8, x9, lsl #3]
10002f2f0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f2f4:     	ldr	w0, [x27, #0x18]
10002f2f8:     	cmp	x21, x0
10002f2fc:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002f300:     	add	x8, x28, x0, lsl #4
10002f304:     	ldp	x0, x1, [x8]
10002f308:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f30c:     	cmp	x5, #0x0
10002f310:     	mov	x1, xzr
10002f314:     	cset	w0, eq
10002f318:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f31c:     	ldr	x8, [sp, #0x18]
10002f320:     	ldr	w0, [x27, #0x18]
10002f324:     	ldr	x1, [x8, #0x78]
10002f328:     	cmp	x1, x0
10002f32c:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f330:     	ldr	x8, [x8, #0x70]
10002f334:     	adds	x8, x8, x0, lsl #3
10002f338:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f33c:     	ldr	x8, [x8]
10002f340:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f344:     	ldr	x0, [x8, #0x10]
10002f348:     	ldr	x9, [x0, #0x40]
10002f34c:     	ldr	x8, [x0, #0x60]
10002f350:     	cbz	x9, 0x10002faf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1458>
10002f354:     	cbnz	x8, 0x10002faf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x145c>
10002f358:     	bl	0x100049de4 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).pagesLocked>
10002f35c:     	ldr	x16, [sp, #0x28]
10002f360:     	ldr	w15, [sp, #0x24]
10002f364:     	mov	x1, xzr
10002f368:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f36c:     	ldr	w0, [x27, #0x18]
10002f370:     	mov	x1, xzr
10002f374:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f378:     	mov	x1, xzr
10002f37c:     	eor	x0, x7, x5
10002f380:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f384:     	mov	x1, xzr
10002f388:     	mov	w0, w5
10002f38c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f390:     	mov	x1, xzr
10002f394:     	lsr	x0, x5, x7
10002f398:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f39c:     	mov	x1, xzr
10002f3a0:     	lsr	w0, w5, w7
10002f3a4:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f3a8:     	mov	x1, xzr
10002f3ac:     	orr	x0, x7, x5
10002f3b0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f3b4:     	mov	x1, xzr
10002f3b8:     	asr	w0, w5, w7
10002f3bc:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f3c0:     	mov	x1, xzr
10002f3c4:     	eor	w0, w7, w5
10002f3c8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f3cc:     	lsr	x8, x5, #1
10002f3d0:     	and	x9, x5, #0x5555555555555555
10002f3d4:     	mov	x1, xzr
10002f3d8:     	and	x8, x8, #0x5555555555555555
10002f3dc:     	add	x8, x8, x9
10002f3e0:     	lsr	x9, x8, #2
10002f3e4:     	and	x8, x8, #0x3333333333333333
10002f3e8:     	and	x9, x9, #0x3333333333333333
10002f3ec:     	add	x8, x9, x8
10002f3f0:     	add	x8, x8, x8, lsr #4
10002f3f4:     	and	x8, x8, #0xf0f0f0f0f0f0f0f
10002f3f8:     	add	x8, x8, x8, lsr #8
10002f3fc:     	add	x8, x8, x8, lsr #16
10002f400:     	add	x8, x8, x8, lsr #32
10002f404:     	and	x0, x8, #0x7f
10002f408:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f40c:     	cmp	x5, x7
10002f410:     	mov	x1, xzr
10002f414:     	cset	w0, lo
10002f418:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f41c:     	cmp	x5, x7
10002f420:     	mov	x1, xzr
10002f424:     	cset	w0, ls
10002f428:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f42c:     	cmp	x5, x7
10002f430:     	mov	x1, xzr
10002f434:     	cset	w0, hi
10002f438:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f43c:     	mov	x1, xzr
10002f440:     	mul	w0, w7, w5
10002f444:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f448:     	lsr	w8, w5, #16
10002f44c:     	mov	x10, #-0x10             ; =-16
10002f450:     	mov	w11, #0x20              ; =32
10002f454:     	mov	w12, #0x18              ; =24
10002f458:     	mov	x1, xzr
10002f45c:     	cmp	w8, #0x0
10002f460:     	csel	w8, w8, w5, ne
10002f464:     	csel	x10, x10, xzr, ne
10002f468:     	lsr	w9, w8, #8
10002f46c:     	cmp	w8, #0xff
10002f470:     	csel	w8, w9, w8, hi
10002f474:     	adrp	x9, 0x1002b0000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x1170>
10002f478:     	add	x9, x9, #0x545
10002f47c:     	and	x8, x8, #0xff
10002f480:     	ldrb	w8, [x9, x8]
10002f484:     	csel	x9, x12, x11, hi
10002f488:     	add	x9, x9, x10
10002f48c:     	sub	x0, x9, x8
10002f490:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f494:     	neg	w8, w7
10002f498:     	mov	x1, xzr
10002f49c:     	ror	w0, w5, w8
10002f4a0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f4a4:     	cmp	x5, x7
10002f4a8:     	mov	x1, xzr
10002f4ac:     	cset	w0, gt
10002f4b0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f4b4:     	cmp	w5, w7
10002f4b8:     	mov	x1, xzr
10002f4bc:     	cset	w0, lt
10002f4c0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f4c4:     	cmp	x5, x7
10002f4c8:     	mov	x1, xzr
10002f4cc:     	cset	w0, ne
10002f4d0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f4d4:     	cbz	w5, 0x10002fb28 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x148c>
10002f4d8:     	neg	w8, w5
10002f4dc:     	mov	w9, #0xb531             ; =46385
10002f4e0:     	mov	x1, xzr
10002f4e4:     	and	w8, w5, w8
10002f4e8:     	movk	w9, #0x77c, lsl #16
10002f4ec:     	mul	w8, w8, w9
10002f4f0:     	adrp	x9, 0x1002b0000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x1170>
10002f4f4:     	add	x9, x9, #0x525
10002f4f8:     	lsr	w8, w8, #27
10002f4fc:     	ldrb	w0, [x9, w8, uxtw]
10002f500:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f504:     	mov	x1, xzr
10002f508:     	add	w0, w7, w5
10002f50c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f510:     	cmp	w5, w7
10002f514:     	mov	x1, xzr
10002f518:     	cset	w0, ne
10002f51c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f520:     	cmp	x5, x7
10002f524:     	mov	x1, xzr
10002f528:     	cset	w0, le
10002f52c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f530:     	mov	x1, xzr
10002f534:     	ror	w0, w5, w7
10002f538:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f53c:     	cmp	w5, w7
10002f540:     	mov	x1, xzr
10002f544:     	cset	w0, ls
10002f548:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f54c:     	cmp	w5, w7
10002f550:     	mov	x1, xzr
10002f554:     	cset	w0, ge
10002f558:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f55c:     	cmp	w5, #0x0
10002f560:     	mov	x1, xzr
10002f564:     	cset	w0, eq
10002f568:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f56c:     	cmp	w5, w7
10002f570:     	mov	x1, xzr
10002f574:     	cset	w0, lo
10002f578:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f57c:     	lsr	w8, w5, #24
10002f580:     	ubfx	x10, x5, #16, #8
10002f584:     	ubfx	x11, x5, #8, #8
10002f588:     	and	x9, x5, #0xff
10002f58c:     	adrp	x12, 0x100295000 <dyld_stub_binder+0x100295000>
10002f590:     	add	x12, x12, #0xaf4
10002f594:     	ldrb	w8, [x12, x8]
10002f598:     	ldrb	w10, [x12, x10]
10002f59c:     	ldrb	w11, [x12, x11]
10002f5a0:     	ldrb	w9, [x12, x9]
10002f5a4:     	mov	x1, xzr
10002f5a8:     	add	w8, w10, w8
10002f5ac:     	add	w9, w11, w9
10002f5b0:     	add	w8, w8, w9
10002f5b4:     	and	x0, x8, #0xff
10002f5b8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f5bc:     	mov	x1, xzr
10002f5c0:     	add	x0, x7, x5
10002f5c4:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f5c8:     	mov	x1, xzr
10002f5cc:     	lsl	w0, w5, w7
10002f5d0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f5d4:     	cmp	x5, #0x0
10002f5d8:     	mov	x1, xzr
10002f5dc:     	cset	w0, eq
10002f5e0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f5e4:     	mov	x1, xzr
10002f5e8:     	and	x0, x7, x5
10002f5ec:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f5f0:     	mov	x1, xzr
10002f5f4:     	sub	x0, x5, x7
10002f5f8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f5fc:     	cmp	w5, w7
10002f600:     	mov	x1, xzr
10002f604:     	cset	w0, eq
10002f608:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f60c:     	neg	w8, w7
10002f610:     	mov	x1, xzr
10002f614:     	ror	x0, x5, x8
10002f618:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f61c:     	cmp	w5, w7
10002f620:     	mov	x1, xzr
10002f624:     	cset	w0, hs
10002f628:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f62c:     	mov	x1, xzr
10002f630:     	and	w0, w5, w7
10002f634:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f638:     	cmp	w5, w7
10002f63c:     	mov	x1, xzr
10002f640:     	cset	w0, le
10002f644:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f648:     	cmp	w5, w7
10002f64c:     	mov	x1, xzr
10002f650:     	cset	w0, gt
10002f654:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f658:     	lsr	x8, x5, #32
10002f65c:     	mov	w10, #0x20              ; =32
10002f660:     	mov	x1, xzr
10002f664:     	cmp	x8, #0x0
10002f668:     	csel	x8, x8, x5, ne
10002f66c:     	csel	x10, x10, xzr, ne
10002f670:     	lsr	x9, x8, #16
10002f674:     	cmp	x9, #0x0
10002f678:     	csel	x8, x9, x8, ne
10002f67c:     	orr	x9, x10, #0x10
10002f680:     	lsr	x11, x8, #8
10002f684:     	csel	x9, x9, x10, ne
10002f688:     	cmp	x8, #0xff
10002f68c:     	orr	x10, x9, #0x8
10002f690:     	csel	x8, x11, x8, hi
10002f694:     	csel	x9, x10, x9, hi
10002f698:     	adrp	x10, 0x1002b0000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x1170>
10002f69c:     	add	x10, x10, #0x545
10002f6a0:     	and	x8, x8, #0xff
10002f6a4:     	ldrb	w8, [x10, x8]
10002f6a8:     	mov	w10, #0x40              ; =64
10002f6ac:     	sub	x9, x10, x9
10002f6b0:     	sub	x0, x9, x8
10002f6b4:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f6b8:     	cmp	x5, x7
10002f6bc:     	mov	x1, xzr
10002f6c0:     	cset	w0, lt
10002f6c4:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f6c8:     	mov	x1, xzr
10002f6cc:     	ror	x0, x5, x7
10002f6d0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f6d4:     	cmp	w5, w7
10002f6d8:     	mov	x1, xzr
10002f6dc:     	cset	w0, hi
10002f6e0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f6e4:     	mov	x1, xzr
10002f6e8:     	mov	w0, w5
10002f6ec:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f6f0:     	mov	x1, xzr
10002f6f4:     	sxth	x0, w5
10002f6f8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f6fc:     	mov	x1, xzr
10002f700:     	sxtw	x0, w5
10002f704:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f708:     	cmp	x5, x7
10002f70c:     	mov	x1, xzr
10002f710:     	cset	w0, eq
10002f714:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f718:     	mov	x1, xzr
10002f71c:     	sxtw	x0, w5
10002f720:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f724:     	cmp	x5, x7
10002f728:     	mov	x1, xzr
10002f72c:     	cset	w0, hs
10002f730:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f734:     	cmp	x5, x7
10002f738:     	mov	x1, xzr
10002f73c:     	cset	w0, ge
10002f740:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f744:     	mov	x1, xzr
10002f748:     	asr	x0, x5, x7
10002f74c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f750:     	mov	x1, xzr
10002f754:     	orr	w0, w7, w5
10002f758:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f75c:     	mov	x1, xzr
10002f760:     	sub	w0, w5, w7
10002f764:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f768:     	mov	x1, xzr
10002f76c:     	mul	x0, x7, x5
10002f770:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f774:     	sxth	x8, w5
10002f778:     	mov	x1, xzr
10002f77c:     	mov	w0, w8
10002f780:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f784:     	rbit	x8, x5
10002f788:     	mov	x1, xzr
10002f78c:     	clz	x0, x8
10002f790:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f794:     	mov	x1, xzr
10002f798:     	lsl	x0, x5, x7
10002f79c:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f7a0:     	mov	x1, xzr
10002f7a4:     	sxtb	x0, w5
10002f7a8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f7ac:     	sxtb	x8, w5
10002f7b0:     	mov	x1, xzr
10002f7b4:     	mov	w0, w8
10002f7b8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f7bc:     	sub	w8, w9, #0x8b
10002f7c0:     	cmp	w8, #0x34
10002f7c4:     	b.hi	0x10002f7dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1140>
10002f7c8:     	mov	x10, #-0x10000001       ; =-268435457
10002f7cc:     	movk	x10, #0xfff9, lsl #32
10002f7d0:     	movk	x10, #0x1f, lsl #48
10002f7d4:     	lsr	x8, x10, x8
10002f7d8:     	tbnz	w8, #0x0, 0x10002eb64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4c8>
10002f7dc:     	sub	w8, w9, #0x61
10002f7e0:     	mov	x0, xzr
10002f7e4:     	cmp	w8, #0x21
10002f7e8:     	b.hi	0x10002fbf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1554>
10002f7ec:     	mov	x10, #0x3c0000000       ; =16106127360
10002f7f0:     	mov	x1, x0
10002f7f4:     	movk	x10, #0xf03f
10002f7f8:     	lsr	x8, x10, x8
10002f7fc:     	tbnz	w8, #0x0, 0x10002eb64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4c8>
10002f800:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f804:     	mov	x0, x8
10002f808:     	mov	w1, w9
10002f80c:     	bl	0x10004a7b0 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).storeLocked>
10002f810:     	mov	w25, w0
10002f814:     	mov	x0, xzr
10002f818:     	mov	x1, xzr
10002f81c:     	b	0x10002f014 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x978>
10002f820:     	mov	x0, x7
10002f824:     	mov	x1, x6
10002f828:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f82c:     	cmp	w9, #0x5
10002f830:     	b.eq	0x10002fd34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1698>
10002f834:     	ldr	w9, [x27, #0x18]
10002f838:     	ldr	x1, [x16, #0xe8]
10002f83c:     	add	w0, w9, w8
10002f840:     	cmp	x1, x0
10002f844:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002f848:     	ldr	x8, [x16, #0xe0]
10002f84c:     	mov	w9, #0xc                ; =12
10002f850:     	umaddl	x8, w0, w9, x8
10002f854:     	ldp	w26, w25, [x8]
10002f858:     	ldr	w27, [x8, #0x8]
10002f85c:     	cmp	w26, w24
10002f860:     	b.hi	0x10002f8c4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1228>
10002f864:     	ldr	x8, [x20, #0x160]
10002f868:     	cbz	x8, 0x10002f878 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11dc>
10002f86c:     	cbz	x20, 0x10002f930 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1294>
10002f870:     	ldar	w8, [x8]
10002f874:     	cbz	w8, 0x10002fb1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1480>
10002f878:     	ldp	x8, x0, [x20, #0x8]
10002f87c:     	cbz	x8, 0x10002fea0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1804>
10002f880:     	ldr	x9, [x8, #0x8]
10002f884:     	ldrb	w10, [x9, #0x17]
10002f888:     	tbz	w10, #0x5, 0x10002f8a4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1208>
10002f88c:     	ldrb	w9, [x9, #0x17]
10002f890:     	and	w9, w9, #0x1f
10002f894:     	cmp	w9, #0x16
10002f898:     	b.eq	0x10002f8a4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1208>
10002f89c:     	stur	x0, [x29, #-0x78]
10002f8a0:     	sub	x0, x29, #0x78
10002f8a4:     	ldr	x8, [x8, #0x28]
10002f8a8:     	blr	x8
10002f8ac:     	ldr	w15, [sp, #0x24]
10002f8b0:     	ldr	x16, [sp, #0x28]
10002f8b4:     	mov	x2, x1
10002f8b8:     	cbz	x0, 0x10002f944 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x12a8>
10002f8bc:     	ldr	x8, [x0, #0x8]
10002f8c0:     	cbnz	x8, 0x10002fcc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1628>
10002f8c4:     	ldr	x8, [x16, #0x108]
10002f8c8:     	add	w2, w27, w25
10002f8cc:     	ldr	x9, [x16, #0xf8]
10002f8d0:     	cmp	x8, x2
10002f8d4:     	b.lo	0x10002fe24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1788>
10002f8d8:     	cmp	w25, w2
10002f8dc:     	b.hi	0x10002fe90 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17f4>
10002f8e0:     	sub	x8, x8, x25
10002f8e4:     	lsl	x10, x25, #3
10002f8e8:     	cmp	x8, #0x0
10002f8ec:     	csel	x8, x10, xzr, gt
10002f8f0:     	cbz	w27, 0x10002e74c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xb0>
10002f8f4:     	add	x8, x9, x8
10002f8f8:     	sub	x9, x2, x25
10002f8fc:     	add	x10, x8, #0x4
10002f900:     	ldr	w0, [x10]
10002f904:     	cmp	x21, x0
10002f908:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002f90c:     	ldur	w8, [x10, #-0x4]
10002f910:     	cmp	x21, x8
10002f914:     	b.ls	0x10002fdfc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1760>
10002f918:     	ldr	q0, [x28, x0, lsl #4]
10002f91c:     	subs	x9, x9, #0x1
10002f920:     	add	x10, x10, #0x8
10002f924:     	str	q0, [x28, x8, lsl #4]
10002f928:     	b.ne	0x10002f900 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1264>
10002f92c:     	b	0x10002e74c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xb0>
10002f930:     	mov	w0, #0x1                ; =1
10002f934:     	bl	0x10020c0b8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002f938:     	ldr	x16, [sp, #0x28]
10002f93c:     	ldr	w15, [sp, #0x24]
10002f940:     	cbnz	x0, 0x10002f8bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1220>
10002f944:     	ldr	x8, [x16, #0x108]
10002f948:     	add	w2, w27, w25
10002f94c:     	ldr	x9, [x16, #0xf8]
10002f950:     	cmp	x8, x2
10002f954:     	b.hs	0x10002f8d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x123c>
10002f958:     	b	0x10002fe24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1788>
10002f95c:     	cmp	w8, #0x3
10002f960:     	b.eq	0x10002fc4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15b0>
10002f964:     	cmp	w8, #0x6
10002f968:     	b.eq	0x10002fbf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f96c:     	sub	w8, w9, #0x12
10002f970:     	cmp	w8, #0x4
10002f974:     	b.lo	0x10002fc24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1588>
10002f978:     	ldr	w0, [x27, #0x14]
10002f97c:     	cmn	w0, #0x1
10002f980:     	b.eq	0x10002f9d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1338>
10002f984:     	cmp	x21, x0
10002f988:     	b.ls	0x10002fde8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x174c>
10002f98c:     	add	x8, x28, x0, lsl #4
10002f990:     	ldp	x10, x11, [x8]
10002f994:     	and	w8, w9, #0xffff0000
10002f998:     	cmp	w8, #0xfe0, lsl #12     ; =0xfe0000
10002f99c:     	b.ne	0x10002f9e8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x134c>
10002f9a0:     	and	w4, w9, #0xffff
10002f9a4:     	cmp	w4, #0x3
10002f9a8:     	b.ne	0x10002fb34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1498>
10002f9ac:     	cbz	x20, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f9b0:     	ldr	x8, [x20]
10002f9b4:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002f9b8:     	ldr	x0, [x8, #0x8]
10002f9bc:     	bl	0x1000493c8 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Domain).Fence>
10002f9c0:     	ldr	x16, [sp, #0x28]
10002f9c4:     	ldr	w15, [sp, #0x24]
10002f9c8:     	mov	x0, xzr
10002f9cc:     	mov	x1, xzr
10002f9d0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002f9d4:     	mov	x10, xzr
10002f9d8:     	mov	x11, xzr
10002f9dc:     	and	w8, w9, #0xffff0000
10002f9e0:     	cmp	w8, #0xfe0, lsl #12     ; =0xfe0000
10002f9e4:     	b.eq	0x10002f9a0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1304>
10002f9e8:     	mov	w8, #0xffed             ; =65517
10002f9ec:     	movk	w8, #0xff03, lsl #16
10002f9f0:     	add	w8, w8, w9
10002f9f4:     	add	w8, w8, #0xb
10002f9f8:     	cmp	w8, #0xa
10002f9fc:     	b.hs	0x10002fed0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1834>
10002fa00:     	ldr	x1, [x16, #0x80]
10002fa04:     	ldr	w0, [x27, #0x4]
10002fa08:     	cmp	x1, x0
10002fa0c:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002fa10:     	ldr	x8, [x16, #0x78]
10002fa14:     	mov	w9, #0xe0               ; =224
10002fa18:     	umaddl	x8, w0, w9, x8
10002fa1c:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002fa20:     	ldr	w9, [x8]
10002fa24:     	ldr	x12, [x8, #0x8]
10002fa28:     	add	x2, sp, #0x80
10002fa2c:     	ldr	q0, [x8, #0x10]
10002fa30:     	ldur	q1, [x8, #0x48]
10002fa34:     	mov	x0, x20
10002fa38:     	str	w9, [sp, #0x80]
10002fa3c:     	ldur	q2, [x8, #0x68]
10002fa40:     	mov	x3, x5
10002fa44:     	str	x12, [sp, #0x88]
10002fa48:     	ldp	x9, x12, [x8, #0x20]
10002fa4c:     	str	q0, [sp, #0x90]
10002fa50:     	mov	x5, x7
10002fa54:     	mov	x7, x10
10002fa58:     	ldr	q0, [x8, #0x30]
10002fa5c:     	ldr	x1, [sp, #0x18]
10002fa60:     	stp	x9, x12, [sp, #0xa0]
10002fa64:     	ldrb	w9, [x8, #0x40]
10002fa68:     	add	x12, sp, #0x80
10002fa6c:     	str	q0, [sp, #0xb0]
10002fa70:     	ldur	q0, [x8, #0x58]
10002fa74:     	strb	w9, [sp, #0xc0]
10002fa78:     	stur	q1, [x12, #0x48]
10002fa7c:     	ldur	q1, [x8, #0x78]
10002fa80:     	stur	q0, [x12, #0x58]
10002fa84:     	ldur	q0, [x8, #0x88]
10002fa88:     	ldp	x9, x8, [x8, #0x98]
10002fa8c:     	stur	q2, [x12, #0x68]
10002fa90:     	stur	q1, [x12, #0x78]
10002fa94:     	stur	q0, [x12, #0x88]
10002fa98:     	stp	x9, x8, [sp, #0x118]
10002fa9c:     	add	x8, sp, #0x58
10002faa0:     	str	x11, [sp]
10002faa4:     	bl	0x100034004 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).bulk>
10002faa8:     	ldp	x8, x2, [sp, #0x70]
10002faac:     	ldrb	w25, [sp, #0x68]
10002fab0:     	ldp	x0, x1, [sp, #0x58]
10002fab4:     	cbz	x8, 0x10002fbc8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x152c>
10002fab8:     	ldr	x9, [x8, #0x8]
10002fabc:     	ldr	w15, [sp, #0x24]
10002fac0:     	ldr	x16, [sp, #0x28]
10002fac4:     	cbz	x9, 0x10002f01c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x980>
10002fac8:     	b	0x10002fdd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1734>
10002facc:     	ldr	x8, [x25, #0x10]
10002fad0:     	mov	x1, xzr
10002fad4:     	ldrb	w8, [x8, #0x31]
10002fad8:     	tst	w8, #0x1
10002fadc:     	strb	w8, [sp, #0x57]
10002fae0:     	mov	w8, #-0x1               ; =-1
10002fae4:     	csinv	x0, x8, xzr, eq
10002fae8:     	ldr	w15, [sp, #0x24]
10002faec:     	ldr	x16, [sp, #0x28]
10002faf0:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002faf4:     	cbz	x8, 0x10002fbcc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1530>
10002faf8:     	ldar	x8, [x8]
10002fafc:     	cbz	x0, 0x10002fbd4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1538>
10002fb00:     	ldr	x9, [x0, #0x38]
10002fb04:     	mov	w10, #0x10000           ; =65536
10002fb08:     	mov	x1, xzr
10002fb0c:     	cmp	x9, #0x0
10002fb10:     	csel	x9, x10, x9, eq
10002fb14:     	udiv	x0, x8, x9
10002fb18:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002fb1c:     	mov	x0, xzr
10002fb20:     	mov	x2, xzr
10002fb24:     	b	0x10002f944 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x12a8>
10002fb28:     	mov	x1, xzr
10002fb2c:     	mov	w0, #0x20               ; =32
10002fb30:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002fb34:     	ldr	x1, [x16, #0x80]
10002fb38:     	ldr	w0, [x27, #0x4]
10002fb3c:     	cmp	x1, x0
10002fb40:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002fb44:     	ldr	x8, [x16, #0x78]
10002fb48:     	mov	w9, #0xe0               ; =224
10002fb4c:     	umaddl	x8, w0, w9, x8
10002fb50:     	cbz	x8, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002fb54:     	ldr	x9, [sp, #0x18]
10002fb58:     	ldr	w0, [x8, #0x18]
10002fb5c:     	ldr	x1, [x9, #0x78]
10002fb60:     	cmp	x1, x0
10002fb64:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002fb68:     	ldr	x9, [x9, #0x70]
10002fb6c:     	adds	x9, x9, x0, lsl #3
10002fb70:     	b.eq	0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002fb74:     	ldr	x9, [x9]
10002fb78:     	cbz	x9, 0x10002fc6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15d0>
10002fb7c:     	ldp	x11, x1, [x20]
10002fb80:     	ldr	x2, [x20, #0x10]
10002fb84:     	ldr	x0, [x9, #0x10]
10002fb88:     	ldr	x3, [x11, #0x8]
10002fb8c:     	ldr	w9, [x8, #0x14]
10002fb90:     	ldr	w8, [x8, #0x1c]
10002fb94:     	str	x10, [sp]
10002fb98:     	orr	x6, x9, x8, lsl #32
10002fb9c:     	sub	x8, x29, #0xf8
10002fba0:     	bl	0x10004b2c0 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).Atomic>
10002fba4:     	ldp	x8, x2, [x29, #-0xe0]
10002fba8:     	ldurb	w25, [x29, #-0xe8]
10002fbac:     	ldp	x0, x1, [x29, #-0xf8]
10002fbb0:     	cbz	x8, 0x10002fbec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1550>
10002fbb4:     	ldr	x9, [x8, #0x8]
10002fbb8:     	ldr	w15, [sp, #0x24]
10002fbbc:     	ldr	x16, [sp, #0x28]
10002fbc0:     	cbz	x9, 0x10002f01c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x980>
10002fbc4:     	b	0x10002fdd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1734>
10002fbc8:     	b	0x10002f014 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x978>
10002fbcc:     	ldr	x8, [x0, #0x8]
10002fbd0:     	cbnz	x0, 0x10002fb00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1464>
10002fbd4:     	mov	w0, #0x1                ; =1
10002fbd8:     	bl	0x10020c0b8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002fbdc:     	ldr	x16, [sp, #0x28]
10002fbe0:     	ldr	w15, [sp, #0x24]
10002fbe4:     	mov	x1, xzr
10002fbe8:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002fbec:     	b	0x10002f014 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x978>
10002fbf0:     	mov	x1, x0
10002fbf4:     	b	0x10002f024 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x988>
10002fbf8:     	ldr	x8, [sp, #0x10]
10002fbfc:     	mov	x0, x20
10002fc00:     	stur	w24, [x8, #-0x38]
10002fc04:     	bl	0x100031bf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runRuntime>
10002fc08:     	tst	w0, #0xff
10002fc0c:     	b.ne	0x10002fcd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1634>
10002fc10:     	ldr	x8, [x20, #0x48]
10002fc14:     	ldr	w15, [sp, #0x24]
10002fc18:     	cmp	x8, #0x0
10002fc1c:     	b.gt	0x10002e6d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x3c>
10002fc20:     	b	0x10002feb8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x181c>
10002fc24:     	ldr	x8, [sp, #0x10]
10002fc28:     	mov	x0, x20
10002fc2c:     	stur	w24, [x8, #-0x38]
10002fc30:     	bl	0x100030cc0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runCall>
10002fc34:     	b	0x10002fc08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x156c>
10002fc38:     	ldr	x8, [sp, #0x10]
10002fc3c:     	mov	x0, x20
10002fc40:     	stur	w24, [x8, #-0x38]
10002fc44:     	bl	0x100031800 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runThrow>
10002fc48:     	b	0x10002fc08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x156c>
10002fc4c:     	ldr	x8, [sp, #0x10]
10002fc50:     	mov	x0, x20
10002fc54:     	stur	w24, [x8, #-0x38]
10002fc58:     	bl	0x1000308d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).returnFrame>
10002fc5c:     	and	w8, w0, #0xff
10002fc60:     	tbz	w8, #0x0, 0x10002fc10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1574>
10002fc64:     	mov	w0, #0x1                ; =1
10002fc68:     	b	0x10002fdac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1710>
10002fc6c:     	mov	w0, #0x1                ; =1
10002fc70:     	bl	0x10020c0b8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002fc74:     	mov	x0, xzr
10002fc78:     	b	0x10002fdac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1710>
10002fc7c:     	mov	w0, #0x28               ; =40
10002fc80:     	bl	0x100220c20 <dyld_stub_binder+0x100220c20>
10002fc84:     	cbz	x0, 0x10002fee8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x184c>
10002fc88:     	mov	x21, x0
10002fc8c:     	mov	w0, #0x28               ; =40
10002fc90:     	bl	0x100207d28 <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002fc94:     	ldr	x8, [x25, #0x8]
10002fc98:     	adrp	x9, 0x1003bc000 <_github.com/xgo-dev/llgo/runtime/internal/runtime.zeroVal+0x14b>
10002fc9c:     	adrp	x10, 0x100295000 <dyld_stub_binder+0x100295000>
10002fca0:     	add	x10, x10, #0xa8a
10002fca4:     	ldr	q0, [x9, #0xcc0]
10002fca8:     	adrp	x1, 0x1002ed000 <__llgo_github.com/cpunion/wasmvm/internal/compiler/ir.BlockID+0x40>
10002fcac:     	add	x1, x1, #0x660
10002fcb0:     	stp	x8, x10, [x21, #0x10]
10002fcb4:     	mov	w8, #0xe                ; =14
10002fcb8:     	str	q0, [x21]
10002fcbc:     	str	x8, [x21, #0x20]
10002fcc0:     	b	0x10002fd9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1700>
10002fcc4:     	mov	x1, x0
10002fcc8:     	mov	x0, x20
10002fccc:     	b	0x10002fda4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1708>
10002fcd0:     	and	x0, x0, #0xff
10002fcd4:     	b	0x10002fdac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1710>
10002fcd8:     	mov	w0, #0x8                ; =8
10002fcdc:     	bl	0x100220c20 <dyld_stub_binder+0x100220c20>
10002fce0:     	cbz	x0, 0x10002fee8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x184c>
10002fce4:     	mov	x21, x0
10002fce8:     	mov	w0, #0x8                ; =8
10002fcec:     	bl	0x100207d28 <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002fcf0:     	adrp	x2, 0x1002d0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fcf4:     	add	x2, x2, #0xa00
10002fcf8:     	mov	x0, x25
10002fcfc:     	mov	x1, x26
10002fd00:     	mov	x3, x21
10002fd04:     	bl	0x1001e60ac <_errors.As>
10002fd08:     	tbz	w0, #0x0, 0x10002fd1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1680>
10002fd0c:     	ldr	x9, [sp, #0x8]
10002fd10:     	ldr	x8, [x21]
10002fd14:     	ldr	w9, [x9, #0x50]
10002fd18:     	str	w9, [x8, #0x4]
10002fd1c:     	mov	x0, x20
10002fd20:     	mov	x1, x25
10002fd24:     	mov	x2, x26
10002fd28:     	b	0x10002fda4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1708>
10002fd2c:     	mov	w25, #0x3               ; =3
10002fd30:     	b	0x10002fd40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16a4>
10002fd34:     	mov	w25, #0x1               ; =1
10002fd38:     	b	0x10002fd40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16a4>
10002fd3c:     	mov	w25, #0x9               ; =9
10002fd40:     	mov	w0, #0x10               ; =16
10002fd44:     	bl	0x100220c14 <dyld_stub_binder+0x100220c14>
10002fd48:     	cbz	x0, 0x10002fee8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x184c>
10002fd4c:     	mov	x21, x0
10002fd50:     	mov	w0, #0x10               ; =16
10002fd54:     	bl	0x100207d28 <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002fd58:     	ldr	x9, [sp, #0x28]
10002fd5c:     	stp	xzr, xzr, [x21]
10002fd60:     	ldr	w0, [x27, #0x4]
10002fd64:     	ldr	x1, [x9, #0x80]
10002fd68:     	cmp	x1, x0
10002fd6c:     	b.ls	0x10002fdf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1754>
10002fd70:     	mov	w8, #0xe0               ; =224
10002fd74:     	ldr	x9, [x9, #0x78]
10002fd78:     	adrp	x1, 0x1002d0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fd7c:     	add	x1, x1, #0xd68
10002fd80:     	umaddl	x8, w0, w8, x9
10002fd84:     	ldr	x9, [sp, #0x8]
10002fd88:     	ldr	w9, [x9, #0x50]
10002fd8c:     	ldr	x8, [x8, #0x8]
10002fd90:     	strb	w25, [x21]
10002fd94:     	str	w9, [x21, #0x4]
10002fd98:     	str	x8, [x21, #0x8]
10002fd9c:     	mov	x0, x20
10002fda0:     	mov	x2, x21
10002fda4:     	bl	0x10002d920 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).fail>
10002fda8:     	mov	w0, #0x3                ; =3
10002fdac:     	mov	x1, x19
10002fdb0:     	add	sp, sp, #0x1d0
10002fdb4:     	ldp	x29, x30, [sp, #0x50]
10002fdb8:     	ldp	x20, x19, [sp, #0x40]
10002fdbc:     	ldp	x22, x21, [sp, #0x30]
10002fdc0:     	ldp	x24, x23, [sp, #0x20]
10002fdc4:     	ldp	x26, x25, [sp, #0x10]
10002fdc8:     	ldp	x28, x27, [sp], #0x60
10002fdcc:     	ret
10002fdd0:     	mov	x0, x20
10002fdd4:     	mov	x1, x8
10002fdd8:     	b	0x10002fda4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1708>
10002fddc:     	mov	x0, x24
10002fde0:     	mov	x1, x22
10002fde4:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fde8:     	mov	x1, x21
10002fdec:     	bl	0x100207e20 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fdf0:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fdf4:     	mov	x1, x22
10002fdf8:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fdfc:     	mov	x0, x8
10002fe00:     	mov	x1, x21
10002fe04:     	bl	0x100207e20 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fe08:     	mov	x0, x8
10002fe0c:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fe10:     	mov	x1, x21
10002fe14:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fe18:     	mov	x0, x8
10002fe1c:     	mov	x1, x21
10002fe20:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fe24:     	mov	x0, x2
10002fe28:     	mov	w1, wzr
10002fe2c:     	mov	x2, x8
10002fe30:     	mov	w3, #0x2                ; =2
10002fe34:     	bl	0x100208b38 <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fe38:     	mov	x0, x9
10002fe3c:     	mov	w1, #0x1                ; =1
10002fe40:     	mov	w3, #0x2                ; =2
10002fe44:     	bl	0x100208b38 <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fe48:     	mov	x0, x8
10002fe4c:     	mov	w1, #0x1                ; =1
10002fe50:     	mov	x2, x9
10002fe54:     	mov	w3, #0x3                ; =3
10002fe58:     	bl	0x100208b38 <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fe5c:     	mov	x0, x9
10002fe60:     	mov	x1, x21
10002fe64:     	bl	0x1002093d4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fe68:     	mov	w0, #0x1                ; =1
10002fe6c:     	bl	0x100207e20 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fe70:     	mov	x0, xzr
10002fe74:     	mov	x1, xzr
10002fe78:     	bl	0x100207e20 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fe7c:     	mov	w0, #0x3                ; =3
10002fe80:     	bl	0x100207e20 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fe84:     	mov	w0, #0x2                ; =2
10002fe88:     	mov	w1, #0x2                ; =2
10002fe8c:     	bl	0x100207e20 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fe90:     	mov	x0, x25
10002fe94:     	mov	w1, wzr
10002fe98:     	mov	w3, #0x3                ; =3
10002fe9c:     	bl	0x100208b38 <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fea0:     	adrp	x0, 0x100344000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8+0x18>
10002fea4:     	add	x0, x0, #0x900
10002fea8:     	adrp	x1, 0x100344000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8+0x18>
10002feac:     	add	x1, x1, #0xb80
10002feb0:     	bl	0x100207b14 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002feb4:     	brk	#0x1
10002feb8:     	adrp	x0, 0x10034e000 <_github.com/xgo-dev/llgo/runtime/abi.kindNames$data+0x188>
10002febc:     	add	x0, x0, #0x28
10002fec0:     	adrp	x1, 0x1002d0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fec4:     	add	x1, x1, #0xa50
10002fec8:     	bl	0x100207b14 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fecc:     	brk	#0x1
10002fed0:     	adrp	x0, 0x10034e000 <_github.com/xgo-dev/llgo/runtime/abi.kindNames$data+0x188>
10002fed4:     	add	x0, x0, #0x28
10002fed8:     	adrp	x1, 0x1002d0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fedc:     	add	x1, x1, #0xa60
10002fee0:     	bl	0x100207b14 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fee4:     	brk	#0x1
10002fee8:     	adrp	x0, 0x10034e000 <_github.com/xgo-dev/llgo/runtime/abi.kindNames$data+0x188>
10002feec:     	add	x0, x0, #0x28
10002fef0:     	adrp	x1, 0x100344000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8+0x18>
10002fef4:     	add	x1, x1, #0x8f0
10002fef8:     	bl	0x100207b14 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fefc:     	brk	#0x1
