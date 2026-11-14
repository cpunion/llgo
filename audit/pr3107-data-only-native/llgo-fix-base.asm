/private/tmp/llgo-issue-audit-results-20261113/new-performance-followups/darwin-symbol-final-native/bin/llgo-fix-base:	file format mach-o arm64

Disassembly of section __TEXT,__text:

000000010002e4b0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop>:
10002e4b0:     	sub	sp, sp, #0x1e0
10002e4b4:     	stp	x28, x27, [sp, #0x180]
10002e4b8:     	stp	x26, x25, [sp, #0x190]
10002e4bc:     	stp	x24, x23, [sp, #0x1a0]
10002e4c0:     	stp	x22, x21, [sp, #0x1b0]
10002e4c4:     	stp	x20, x19, [sp, #0x1c0]
10002e4c8:     	stp	x29, x30, [sp, #0x1d0]
10002e4cc:     	add	x29, sp, #0x1d0
10002e4d0:     	ldr	x8, [x0, #0x48]
10002e4d4:     	cmp	x8, #0x1
10002e4d8:     	b.lt	0x10002fc64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17b4>
10002e4dc:     	ldrb	w26, [x0, #0x20]
10002e4e0:     	mov	x20, x0
10002e4e4:     	mov	x19, x1
10002e4e8:     	str	w26, [sp, #0x14]
10002e4ec:     	ldr	x9, [x20, #0x40]
10002e4f0:     	add	x8, x9, x8, lsl #6
10002e4f4:     	ldur	x10, [x8, #-0x40]
10002e4f8:     	sturb	wzr, [x8, #-0x30]
10002e4fc:     	str	x8, [sp, #0x18]
10002e500:     	ldur	x8, [x8, #-0x28]
10002e504:     	ldr	x2, [x20, #0x88]
10002e508:     	ldr	x15, [x10, #0x48]
10002e50c:     	ldr	x21, [x15, #0x98]
10002e510:     	add	x9, x21, x8
10002e514:     	cmp	x9, x2
10002e518:     	b.hi	0x10002fbe4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1734>
10002e51c:     	cmp	x8, x9
10002e520:     	b.hi	0x10002fbf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1744>
10002e524:     	ldr	x9, [sp, #0x18]
10002e528:     	ldr	x22, [x15, #0xb8]
10002e52c:     	ldur	w0, [x9, #-0x38]
10002e530:     	sub	x9, x2, x8
10002e534:     	lsl	x8, x8, #4
10002e538:     	cmp	x9, #0x0
10002e53c:     	csel	x8, x8, xzr, gt
10002e540:     	cmp	x22, x0
10002e544:     	b.ls	0x10002fba0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f0>
10002e548:     	ldr	x9, [x20, #0x78]
10002e54c:     	ldr	x23, [x15, #0xb0]
10002e550:     	mov	w24, w0
10002e554:     	str	x10, [sp, #0x8]
10002e558:     	ldr	x10, [x10, #0x40]
10002e55c:     	add	x28, x9, x8
10002e560:     	stp	x10, x15, [sp, #0x20]
10002e564:     	b	0x10002e584 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xd4>
10002e568:     	add	x8, x28, x8, lsl #4
10002e56c:     	stp	x0, x1, [x8]
10002e570:     	add	w27, w24, #0x1
10002e574:     	mov	w0, w27
10002e578:     	mov	w24, w27
10002e57c:     	cmp	x22, x0
10002e580:     	b.ls	0x10002fba0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f0>
10002e584:     	add	x27, x23, x0, lsl #5
10002e588:     	tbz	w26, #0x0, 0x10002e5c8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x118>
10002e58c:     	ldr	x1, [x15, #0x80]
10002e590:     	ldr	w0, [x27, #0x4]
10002e594:     	cmp	x1, x0
10002e598:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e59c:     	mov	w9, #0xe0               ; =224
10002e5a0:     	ldr	x8, [x15, #0x78]
10002e5a4:     	umaddl	x25, w0, w9, x8
10002e5a8:     	ldr	w8, [x25]
10002e5ac:     	cmp	w8, #0x5
10002e5b0:     	ccmp	w8, #0xb, #0x4, ne
10002e5b4:     	ldr	x8, [x20, #0x150]
10002e5b8:     	cset	w9, ne
10002e5bc:     	subs	x8, x8, x9
10002e5c0:     	b.lo	0x10002fa1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x156c>
10002e5c4:     	str	x8, [x20, #0x150]
10002e5c8:     	add	x19, x19, #0x1
10002e5cc:     	tst	x19, #0x3f
10002e5d0:     	b.ne	0x10002e654 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e5d4:     	ldr	x8, [x20, #0x160]
10002e5d8:     	cbz	x8, 0x10002e5e8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x138>
10002e5dc:     	cbz	x20, 0x10002e62c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17c>
10002e5e0:     	ldar	w8, [x8]
10002e5e4:     	cbz	w8, 0x10002e640 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x190>
10002e5e8:     	ldr	x8, [x20, #0x8]
10002e5ec:     	cbz	x8, 0x10002fc4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x179c>
10002e5f0:     	ldr	x9, [x8, #0x8]
10002e5f4:     	ldr	x0, [x20, #0x10]
10002e5f8:     	ldrb	w9, [x9, #0x17]
10002e5fc:     	tbz	w9, #0x5, 0x10002e614 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x164>
10002e600:     	and	w9, w9, #0x1f
10002e604:     	cmp	w9, #0x16
10002e608:     	b.eq	0x10002e614 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x164>
10002e60c:     	stur	x0, [x29, #-0x78]
10002e610:     	sub	x0, x29, #0x78
10002e614:     	ldr	x8, [x8, #0x28]
10002e618:     	blr	x8
10002e61c:     	ldr	x15, [sp, #0x28]
10002e620:     	mov	x2, x1
10002e624:     	cbnz	x0, 0x10002e64c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x19c>
10002e628:     	b	0x10002e654 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e62c:     	mov	w0, #0x1                ; =1
10002e630:     	bl	0x10021aef4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002e634:     	ldr	x15, [sp, #0x28]
10002e638:     	cbnz	x0, 0x10002e64c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x19c>
10002e63c:     	b	0x10002e654 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e640:     	mov	x0, xzr
10002e644:     	mov	x2, xzr
10002e648:     	b	0x10002e654 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e64c:     	ldr	x8, [x0, #0x8]
10002e650:     	cbnz	x8, 0x10002fa64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15b4>
10002e654:     	ldr	w10, [x27, #0x1c]
10002e658:     	cbz	w10, 0x10002e6d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e65c:     	tbz	w26, #0x0, 0x10002e678 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1c8>
10002e660:     	ldr	w9, [x27]
10002e664:     	cmp	w9, #0x20
10002e668:     	b.eq	0x10002e6d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e66c:     	cmp	w9, #0x41
10002e670:     	b.eq	0x10002e6d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e674:     	b	0x10002e6d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x228>
10002e678:     	ldr	w9, [x27]
10002e67c:     	cmp	w9, #0x20
10002e680:     	b.eq	0x10002e68c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1dc>
10002e684:     	cmp	w9, #0x41
10002e688:     	b.ne	0x10002e6d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x228>
10002e68c:     	mvn	w8, w19
10002e690:     	tst	x8, #0x3f
10002e694:     	b.eq	0x10002e6d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e698:     	cmp	w9, #0x41
10002e69c:     	b.ne	0x10002e6b8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x208>
10002e6a0:     	ldr	w0, [x27, #0x8]
10002e6a4:     	cmp	x21, x0
10002e6a8:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e6ac:     	ldr	w8, [x27, #0x18]
10002e6b0:     	add	x9, x28, x0, lsl #4
10002e6b4:     	stp	x8, xzr, [x9]
10002e6b8:     	add	w24, w24, #0x1
10002e6bc:     	cmp	x22, x24
10002e6c0:     	b.ls	0x10002fb88 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16d8>
10002e6c4:     	add	x27, x23, x24, lsl #5
10002e6c8:     	add	x19, x19, #0x1
10002e6cc:     	ldr	w10, [x27, #0x1c]
10002e6d0:     	cbnz	w10, 0x10002e678 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1c8>
10002e6d4:     	ldr	w9, [x27]
10002e6d8:     	mov	w11, #0xff20            ; =65312
10002e6dc:     	sub	w8, w9, #0xe0
10002e6e0:     	movk	w11, #0xff, lsl #16
10002e6e4:     	cmp	w8, w11
10002e6e8:     	b.hs	0x10002e904 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x454>
10002e6ec:     	lsr	w8, w9, #16
10002e6f0:     	cmp	w8, #0xfb
10002e6f4:     	b.eq	0x10002e864 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x3b4>
10002e6f8:     	cmp	w8, #0xfc
10002e6fc:     	b.eq	0x10002e770 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x2c0>
10002e700:     	cmp	w8, #0xfd
10002e704:     	b.ne	0x10002e8fc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x44c>
10002e708:     	ldr	w0, [x27, #0xc]
10002e70c:     	cmn	w0, #0x1
10002e710:     	b.eq	0x10002eb2c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x67c>
10002e714:     	cmp	x21, x0
10002e718:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e71c:     	add	x8, x28, x0, lsl #4
10002e720:     	ldp	x2, x8, [x8]
10002e724:     	ldr	w0, [x27, #0x10]
10002e728:     	cmn	w0, #0x1
10002e72c:     	b.eq	0x10002eb40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x690>
10002e730:     	cmp	x21, x0
10002e734:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e738:     	add	x10, x28, x0, lsl #4
10002e73c:     	ldp	x5, x6, [x10]
10002e740:     	ldr	w0, [x27, #0x14]
10002e744:     	cmn	w0, #0x1
10002e748:     	b.eq	0x10002eb54 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6a4>
10002e74c:     	cmp	x21, x0
10002e750:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e754:     	add	x11, x28, x0, lsl #4
10002e758:     	ldp	x10, x11, [x11]
10002e75c:     	ldr	x1, [x15, #0x80]
10002e760:     	ldr	w0, [x27, #0x4]
10002e764:     	cmp	x1, x0
10002e768:     	b.hi	0x10002eb6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6bc>
10002e76c:     	b	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e770:     	mov	w8, #0xffed             ; =65517
10002e774:     	movk	w8, #0xff03, lsl #16
10002e778:     	add	w11, w9, w8
10002e77c:     	cmp	w11, #0x4
10002e780:     	b.hs	0x10002e904 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x454>
10002e784:     	ldr	w0, [x27, #0x18]
10002e788:     	ldr	x8, [x15, #0xd8]
10002e78c:     	add	w10, w10, #0x2
10002e790:     	add	w2, w10, w0
10002e794:     	cmp	x8, x2
10002e798:     	b.lo	0x10002fbd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1720>
10002e79c:     	cmp	w0, w2
10002e7a0:     	b.hi	0x10002fc40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1790>
10002e7a4:     	sub	x8, x8, x0
10002e7a8:     	lsl	x12, x0, #2
10002e7ac:     	cmp	x8, #0x0
10002e7b0:     	csel	x8, x12, xzr, gt
10002e7b4:     	cbz	w10, 0x10002fc1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x176c>
10002e7b8:     	ldr	x10, [x15, #0xc8]
10002e7bc:     	add	x10, x10, x8
10002e7c0:     	ldr	w8, [x10]
10002e7c4:     	cmp	x21, x8
10002e7c8:     	b.ls	0x10002fbc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1714>
10002e7cc:     	sub	x1, x2, x0
10002e7d0:     	cmp	x1, #0x1
10002e7d4:     	b.eq	0x10002fc14 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1764>
10002e7d8:     	ldr	w0, [x10, #0x4]
10002e7dc:     	cmp	x21, x0
10002e7e0:     	b.ls	0x10002fbbc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170c>
10002e7e4:     	lsl	x8, x8, #4
10002e7e8:     	lsl	x12, x0, #4
10002e7ec:     	cmp	w11, #0x2
10002e7f0:     	ldr	x14, [x28, x8]
10002e7f4:     	ldr	x13, [x28, x12]
10002e7f8:     	b.hs	0x10002ec5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7ac>
10002e7fc:     	cmp	x1, #0x3
10002e800:     	b.lo	0x10002fc30 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1780>
10002e804:     	ldr	w0, [x10, #0x8]
10002e808:     	cmp	x21, x0
10002e80c:     	b.ls	0x10002fbbc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170c>
10002e810:     	cmp	x1, #0x3
10002e814:     	b.eq	0x10002fc28 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1778>
10002e818:     	ldr	w8, [x10, #0xc]
10002e81c:     	cmp	x21, x8
10002e820:     	b.ls	0x10002fbc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1714>
10002e824:     	lsl	x11, x0, #4
10002e828:     	lsl	x8, x8, #4
10002e82c:     	ldr	x12, [x28, x11]
10002e830:     	mov	w11, #0x13              ; =19
10002e834:     	ldr	x8, [x28, x8]
10002e838:     	movk	w11, #0xfc, lsl #16
10002e83c:     	cmp	w9, w11
10002e840:     	b.ne	0x10002ee18 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x968>
10002e844:     	add	x11, x12, x14
10002e848:     	orr	x9, x12, x14
10002e84c:     	and	x12, x12, x14
10002e850:     	bic	x9, x9, x11
10002e854:     	orr	x9, x9, x12
10002e858:     	add	x9, x13, x9, lsr #63
10002e85c:     	add	x12, x9, x8
10002e860:     	b	0x10002ee34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x984>
10002e864:     	sub	w8, w9, #0x2
10002e868:     	and	w9, w8, #0xffff
10002e86c:     	cmp	w9, #0x20
10002e870:     	b.hi	0x10002ea90 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5e0>
10002e874:     	mov	x9, #0x3e0f             ; =15887
10002e878:     	movk	x9, #0x1c00, lsl #16
10002e87c:     	movk	x9, #0x1, lsl #32
10002e880:     	lsr	x9, x9, x8
10002e884:     	tbz	w9, #0x0, 0x10002ea90 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5e0>
10002e888:     	ldr	w0, [x27, #0xc]
10002e88c:     	cmn	w0, #0x1
10002e890:     	b.eq	0x10002ed08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x858>
10002e894:     	cmp	x21, x0
10002e898:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e89c:     	add	x9, x28, x0, lsl #4
10002e8a0:     	ldp	x3, x4, [x9]
10002e8a4:     	ldr	w0, [x27, #0x10]
10002e8a8:     	cmn	w0, #0x1
10002e8ac:     	b.eq	0x10002ed1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x86c>
10002e8b0:     	cmp	x21, x0
10002e8b4:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e8b8:     	add	x9, x28, x0, lsl #4
10002e8bc:     	ldp	x5, x6, [x9]
10002e8c0:     	and	w8, w8, #0xffff
10002e8c4:     	cmp	w8, #0xc
10002e8c8:     	b.ne	0x10002ed30 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x880>
10002e8cc:     	ldr	w8, [x27, #0x18]
10002e8d0:     	ldr	x1, [x15, #0xd0]
10002e8d4:     	add	w0, w8, #0x2
10002e8d8:     	cmp	x1, x0
10002e8dc:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e8e0:     	ldr	x8, [x15, #0xc8]
10002e8e4:     	ldr	w0, [x8, x0, lsl #2]
10002e8e8:     	cmp	x21, x0
10002e8ec:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e8f0:     	add	x8, x28, x0, lsl #4
10002e8f4:     	ldp	x7, x9, [x8]
10002e8f8:     	b	0x10002ed38 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x888>
10002e8fc:     	cmp	w9, #0xe7
10002e900:     	b.lo	0x10002f99c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14ec>
10002e904:     	ldr	w0, [x27, #0xc]
10002e908:     	cmn	w0, #0x1
10002e90c:     	b.eq	0x10002e98c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4dc>
10002e910:     	cmp	x21, x0
10002e914:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e918:     	add	x8, x28, x0, lsl #4
10002e91c:     	ldp	x5, x4, [x8]
10002e920:     	ldr	w0, [x27, #0x10]
10002e924:     	cmn	w0, #0x1
10002e928:     	b.eq	0x10002e9a0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4f0>
10002e92c:     	cmp	x21, x0
10002e930:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e934:     	add	x8, x28, x0, lsl #4
10002e938:     	ldp	x7, x6, [x8]
10002e93c:     	sub	w8, w9, #0x45
10002e940:     	cmp	w8, #0x7f
10002e944:     	b.hi	0x10002e9b4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x504>
10002e948:     	b.hi	0x10002f554 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x10a4>
10002e94c:     	adrp	x12, 0x100232000 <dyld_stub_binder+0x100232000>
10002e950:     	add	x12, x12, #0x1e8
10002e954:     	adr	x10, 0x10002e964 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b4>
10002e958:     	ldrh	w11, [x12, x8, lsl #1]
10002e95c:     	add	x10, x10, x11, lsl #2
10002e960:     	br	x10
10002e964:     	mov	w0, w9
10002e968:     	mov	x1, x5
10002e96c:     	mov	x2, x7
10002e970:     	bl	0x1000546b8 <_github.com/cpunion/wasmvm/internal/engine/numeric.Eval>
10002e974:     	ldr	x15, [sp, #0x28]
10002e978:     	mov	x25, x1
10002e97c:     	tst	w25, #0xff
10002e980:     	b.ne	0x10002fae0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002e984:     	mov	x1, xzr
10002e988:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002e98c:     	mov	x5, xzr
10002e990:     	mov	x4, xzr
10002e994:     	ldr	w0, [x27, #0x10]
10002e998:     	cmn	w0, #0x1
10002e99c:     	b.ne	0x10002e92c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x47c>
10002e9a0:     	mov	x7, xzr
10002e9a4:     	mov	x6, xzr
10002e9a8:     	sub	w8, w9, #0x45
10002e9ac:     	cmp	w8, #0x7f
10002e9b0:     	b.ls	0x10002e948 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x498>
10002e9b4:     	sub	w8, w9, #0x1
10002e9b8:     	cmp	w8, #0xd3
10002e9bc:     	b.hi	0x10002ea64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5b4>
10002e9c0:     	adrp	x13, 0x100232000 <dyld_stub_binder+0x100232000>
10002e9c4:     	add	x13, x13, #0x40
10002e9c8:     	mov	x0, xzr
10002e9cc:     	adr	x11, 0x10002e9e0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x530>
10002e9d0:     	ldrh	w12, [x13, x8, lsl #1]
10002e9d4:     	add	x11, x11, x12, lsl #2
10002e9d8:     	mov	x1, xzr
10002e9dc:     	br	x11
10002e9e0:     	ldr	x1, [x15, #0x80]
10002e9e4:     	ldr	w0, [x27, #0x4]
10002e9e8:     	cmp	x1, x0
10002e9ec:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e9f0:     	ldr	x8, [x15, #0x78]
10002e9f4:     	mov	w10, #0xe0              ; =224
10002e9f8:     	umaddl	x8, w0, w10, x8
10002e9fc:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ea00:     	ldr	x10, [sp, #0x20]
10002ea04:     	ldr	w0, [x8, #0x18]
10002ea08:     	ldr	x1, [x10, #0x78]
10002ea0c:     	cmp	x1, x0
10002ea10:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ea14:     	ldr	x10, [x10, #0x70]
10002ea18:     	adds	x10, x10, x0, lsl #3
10002ea1c:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ea20:     	ldr	x10, [x10]
10002ea24:     	cbz	x10, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ea28:     	ldr	x0, [x10, #0x10]
10002ea2c:     	ldr	w10, [x8, #0x14]
10002ea30:     	ldr	w8, [x8, #0x1c]
10002ea34:     	ldr	x11, [x0, #0x40]
10002ea38:     	orr	x3, x10, x8, lsl #32
10002ea3c:     	cbz	x11, 0x10002ea48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x598>
10002ea40:     	ldr	x8, [x0, #0x60]
10002ea44:     	cbz	x8, 0x10002edc8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x918>
10002ea48:     	sub	x8, x29, #0x78
10002ea4c:     	mov	w1, w9
10002ea50:     	mov	x2, x5
10002ea54:     	mov	w4, wzr
10002ea58:     	mov	x5, xzr
10002ea5c:     	mov	x6, xzr
10002ea60:     	b	0x10002ebfc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x74c>
10002ea64:     	sub	w8, w9, #0xfc0, lsl #12 ; =0xfc0000
10002ea68:     	cmp	w8, #0x8
10002ea6c:     	b.hs	0x10002ec08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x758>
10002ea70:     	mov	w0, w9
10002ea74:     	mov	x1, x5
10002ea78:     	mov	x2, x7
10002ea7c:     	bl	0x1000546b8 <_github.com/cpunion/wasmvm/internal/engine/numeric.Eval>
10002ea80:     	ldr	x15, [sp, #0x28]
10002ea84:     	mov	x25, x1
10002ea88:     	mov	x1, xzr
10002ea8c:     	b	0x10002edf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x948>
10002ea90:     	ldr	x8, [sp, #0x18]
10002ea94:     	mov	x0, x20
10002ea98:     	stur	w24, [x8, #-0x38]
10002ea9c:     	bl	0x10002fea0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runGC>
10002eaa0:     	ldr	x15, [sp, #0x28]
10002eaa4:     	tst	w0, #0xff
10002eaa8:     	b.eq	0x10002eda0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8f0>
10002eaac:     	b	0x10002fa70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15c0>
10002eab0:     	ldr	x1, [x15, #0x80]
10002eab4:     	ldr	w0, [x27, #0x4]
10002eab8:     	cmp	x1, x0
10002eabc:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002eac0:     	ldr	x8, [x15, #0x78]
10002eac4:     	mov	w10, #0xe0              ; =224
10002eac8:     	umaddl	x8, w0, w10, x8
10002eacc:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ead0:     	ldr	x10, [sp, #0x20]
10002ead4:     	ldr	w0, [x8, #0x18]
10002ead8:     	ldr	x1, [x10, #0x78]
10002eadc:     	cmp	x1, x0
10002eae0:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002eae4:     	ldr	x10, [x10, #0x70]
10002eae8:     	adds	x10, x10, x0, lsl #3
10002eaec:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eaf0:     	ldr	x10, [x10]
10002eaf4:     	cbz	x10, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eaf8:     	ldr	x0, [x10, #0x10]
10002eafc:     	ldr	w10, [x8, #0x14]
10002eb00:     	ldr	w8, [x8, #0x1c]
10002eb04:     	ldr	x11, [x0, #0x40]
10002eb08:     	orr	x3, x10, x8, lsl #32
10002eb0c:     	cbz	x11, 0x10002eb18 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x668>
10002eb10:     	ldr	x8, [x0, #0x60]
10002eb14:     	cbz	x8, 0x10002eed0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa20>
10002eb18:     	mov	w1, w9
10002eb1c:     	mov	x2, x5
10002eb20:     	mov	w4, wzr
10002eb24:     	mov	x5, x7
10002eb28:     	b	0x10002ecf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x840>
10002eb2c:     	mov	x8, xzr
10002eb30:     	mov	x2, xzr
10002eb34:     	ldr	w0, [x27, #0x10]
10002eb38:     	cmn	w0, #0x1
10002eb3c:     	b.ne	0x10002e730 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x280>
10002eb40:     	mov	x5, xzr
10002eb44:     	mov	x6, xzr
10002eb48:     	ldr	w0, [x27, #0x14]
10002eb4c:     	cmn	w0, #0x1
10002eb50:     	b.ne	0x10002e74c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x29c>
10002eb54:     	mov	x10, xzr
10002eb58:     	mov	x11, xzr
10002eb5c:     	ldr	x1, [x15, #0x80]
10002eb60:     	ldr	w0, [x27, #0x4]
10002eb64:     	cmp	x1, x0
10002eb68:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002eb6c:     	mov	w13, #0xe0              ; =224
10002eb70:     	ldr	x12, [x15, #0x78]
10002eb74:     	umaddl	x0, w0, w13, x12
10002eb78:     	cbz	x0, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eb7c:     	and	w12, w9, #0xffff
10002eb80:     	cmp	w12, #0xb
10002eb84:     	b.lo	0x10002eba4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6f4>
10002eb88:     	and	w12, w9, #0xfffc
10002eb8c:     	cmp	w12, #0x54
10002eb90:     	b.eq	0x10002eba4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6f4>
10002eb94:     	and	w13, w9, #0xffff
10002eb98:     	sub	w14, w13, #0x5c
10002eb9c:     	cmp	w14, #0x2
10002eba0:     	b.hs	0x10002ec8c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7dc>
10002eba4:     	ldr	x10, [sp, #0x20]
10002eba8:     	ldr	w8, [x0, #0x18]
10002ebac:     	ldr	x1, [x10, #0x78]
10002ebb0:     	cmp	x1, x8
10002ebb4:     	b.ls	0x10002fbb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002ebb8:     	ldr	x10, [x10, #0x70]
10002ebbc:     	adds	x8, x10, x8, lsl #3
10002ebc0:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ebc4:     	ldr	x8, [x8]
10002ebc8:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ebcc:     	ldr	x10, [x8, #0x10]
10002ebd0:     	ldr	w8, [x0, #0x14]
10002ebd4:     	ldr	w11, [x0, #0x1c]
10002ebd8:     	ldrb	w4, [x0, #0x40]
10002ebdc:     	ldr	x12, [x10, #0x40]
10002ebe0:     	orr	x3, x8, x11, lsl #32
10002ebe4:     	cbz	x12, 0x10002ebf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x740>
10002ebe8:     	ldr	x8, [x10, #0x60]
10002ebec:     	cbz	x8, 0x10002eda8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8f8>
10002ebf0:     	sub	x8, x29, #0x78
10002ebf4:     	mov	x0, x10
10002ebf8:     	mov	w1, w9
10002ebfc:     	sub	x26, x29, #0x78
10002ec00:     	bl	0x10004c83c <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).load>
10002ec04:     	b	0x10002ede8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x938>
10002ec08:     	mov	w8, #-0x1000001         ; =-16777217
10002ec0c:     	add	w8, w9, w8
10002ec10:     	cmp	w8, #0x8
10002ec14:     	b.hi	0x10002f698 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11e8>
10002ec18:     	mov	w11, #0x1               ; =1
10002ec1c:     	mov	w12, #0x117             ; =279
10002ec20:     	lsl	w11, w11, w8
10002ec24:     	tst	w11, w12
10002ec28:     	b.eq	0x10002f688 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11d8>
10002ec2c:     	and	w9, w9, #0xff
10002ec30:     	mov	w8, wzr
10002ec34:     	cmp	w9, #0x4
10002ec38:     	b.gt	0x10002ef04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa54>
10002ec3c:     	cmp	w9, #0x2
10002ec40:     	b.eq	0x10002ef54 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xaa4>
10002ec44:     	cmp	w9, #0x3
10002ec48:     	b.ne	0x10002f5dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ec4c:     	sub	w8, w10, #0x1
10002ec50:     	cmp	w8, w5
10002ec54:     	csel	w8, w8, w5, lo
10002ec58:     	b	0x10002f5dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ec5c:     	umulh	x12, x13, x14
10002ec60:     	mov	w8, #0x13               ; =19
10002ec64:     	movk	w8, #0xfc, lsl #16
10002ec68:     	mul	x11, x13, x14
10002ec6c:     	add	w8, w8, #0x2
10002ec70:     	cmp	w9, w8
10002ec74:     	b.ne	0x10002ee34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x984>
10002ec78:     	and	x8, x13, x14, asr #63
10002ec7c:     	and	x9, x14, x13, asr #63
10002ec80:     	add	x8, x9, x8
10002ec84:     	sub	x12, x12, x8
10002ec88:     	b	0x10002ee34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x984>
10002ec8c:     	cmp	w13, #0xb
10002ec90:     	ldr	x13, [sp, #0x20]
10002ec94:     	b.eq	0x10002eca0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7f0>
10002ec98:     	cmp	w12, #0x58
10002ec9c:     	b.ne	0x10002ef30 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa80>
10002eca0:     	ldr	x1, [x13, #0x78]
10002eca4:     	ldr	w8, [x0, #0x18]
10002eca8:     	cmp	x1, x8
10002ecac:     	b.ls	0x10002fbb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002ecb0:     	ldr	x10, [x13, #0x70]
10002ecb4:     	adds	x8, x10, x8, lsl #3
10002ecb8:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ecbc:     	ldr	x8, [x8]
10002ecc0:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ecc4:     	ldr	x8, [x8, #0x10]
10002ecc8:     	ldr	w10, [x0, #0x14]
10002eccc:     	ldr	w11, [x0, #0x1c]
10002ecd0:     	ldrb	w4, [x0, #0x40]
10002ecd4:     	ldr	x12, [x8, #0x40]
10002ecd8:     	orr	x3, x10, x11, lsl #32
10002ecdc:     	cbz	x12, 0x10002ece8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x838>
10002ece0:     	ldr	x10, [x8, #0x60]
10002ece4:     	cbz	x10, 0x10002f5ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x10fc>
10002ece8:     	mov	x0, x8
10002ecec:     	mov	w1, w9
10002ecf0:     	bl	0x10004a190 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).store>
10002ecf4:     	mov	w8, w0
10002ecf8:     	mov	x0, xzr
10002ecfc:     	mov	x1, xzr
10002ed00:     	and	w25, w8, #0xff
10002ed04:     	b	0x10002edf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002ed08:     	mov	x3, xzr
10002ed0c:     	mov	x4, xzr
10002ed10:     	ldr	w0, [x27, #0x10]
10002ed14:     	cmn	w0, #0x1
10002ed18:     	b.ne	0x10002e8b0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x400>
10002ed1c:     	mov	x5, xzr
10002ed20:     	mov	x6, xzr
10002ed24:     	and	w8, w8, #0xffff
10002ed28:     	cmp	w8, #0xc
10002ed2c:     	b.eq	0x10002e8cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x41c>
10002ed30:     	mov	x7, xzr
10002ed34:     	mov	x9, xzr
10002ed38:     	cbz	x20, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ed3c:     	ldr	x1, [x15, #0x80]
10002ed40:     	ldr	w0, [x27, #0x4]
10002ed44:     	cmp	x1, x0
10002ed48:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ed4c:     	mov	w10, #0xe0              ; =224
10002ed50:     	ldr	x8, [x15, #0x78]
10002ed54:     	umaddl	x2, w0, w10, x8
10002ed58:     	cbz	x2, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ed5c:     	ldr	x0, [x20]
10002ed60:     	ldr	x1, [sp, #0x20]
10002ed64:     	add	x8, sp, #0x30
10002ed68:     	str	x9, [sp]
10002ed6c:     	bl	0x10003a1c4 <_github.com/cpunion/wasmvm/internal/engine/state.(*Store).GCAccess>
10002ed70:     	ldr	x25, [sp, #0x40]
10002ed74:     	ldr	q0, [sp, #0x30]
10002ed78:     	cbz	x25, 0x10002ed84 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8d4>
10002ed7c:     	ldr	x8, [x25, #0x8]
10002ed80:     	cbnz	x8, 0x10002fa78 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15c8>
10002ed84:     	ldr	w0, [x27, #0x8]
10002ed88:     	ldr	x15, [sp, #0x28]
10002ed8c:     	cmn	w0, #0x1
10002ed90:     	b.eq	0x10002eda0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8f0>
10002ed94:     	cmp	x21, x0
10002ed98:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002ed9c:     	str	q0, [x28, x0, lsl #4]
10002eda0:     	add	w27, w24, #0x1
10002eda4:     	b	0x10002ee78 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x9c8>
10002eda8:     	sub	x8, x29, #0x78
10002edac:     	mov	x0, x10
10002edb0:     	mov	w1, w9
10002edb4:     	b	0x10002ede0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x930>
10002edb8:     	ldr	w8, [x27, #0x18]
10002edbc:     	mov	x1, xzr
10002edc0:     	orr	x0, x8, x10, lsl #32
10002edc4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002edc8:     	sub	x8, x29, #0x78
10002edcc:     	mov	w1, w9
10002edd0:     	mov	x2, x5
10002edd4:     	mov	w4, wzr
10002edd8:     	mov	x5, xzr
10002eddc:     	mov	x6, xzr
10002ede0:     	sub	x26, x29, #0x78
10002ede4:     	bl	0x10004c9dc <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).loadLocked>
10002ede8:     	ldp	x0, x1, [x26]
10002edec:     	ldrb	w25, [x26, #0x10]
10002edf0:     	ldr	w26, [sp, #0x14]
10002edf4:     	ldr	x15, [sp, #0x28]
10002edf8:     	tst	w25, #0xff
10002edfc:     	b.ne	0x10002fae0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002ee00:     	ldr	w8, [x27, #0x8]
10002ee04:     	cmn	w8, #0x1
10002ee08:     	b.eq	0x10002e570 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc0>
10002ee0c:     	cmp	x21, x8
10002ee10:     	b.hi	0x10002e568 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xb8>
10002ee14:     	b	0x10002fba8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f8>
10002ee18:     	sub	x11, x14, x12
10002ee1c:     	eon	x9, x14, x12
10002ee20:     	bic	x12, x12, x14
10002ee24:     	and	x9, x11, x9
10002ee28:     	orr	x9, x9, x12
10002ee2c:     	add	x9, x13, x9, asr #63
10002ee30:     	sub	x12, x9, x8
10002ee34:     	ldr	w0, [x27, #0x1c]
10002ee38:     	cmp	x1, x0
10002ee3c:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ee40:     	add	w8, w0, #0x1
10002ee44:     	cmp	x1, x8
10002ee48:     	b.ls	0x10002fbb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002ee4c:     	ldr	w9, [x10, x0, lsl #2]
10002ee50:     	cmp	x21, x9
10002ee54:     	b.ls	0x10002fc08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1758>
10002ee58:     	ldr	w0, [x10, x8, lsl #2]
10002ee5c:     	add	x8, x28, x9, lsl #4
10002ee60:     	cmp	x21, x0
10002ee64:     	stp	x11, xzr, [x8]
10002ee68:     	b.ls	0x10002fbbc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170c>
10002ee6c:     	add	x8, x28, x0, lsl #4
10002ee70:     	add	w27, w24, #0x1
10002ee74:     	stp	x12, xzr, [x8]
10002ee78:     	ldr	x8, [sp, #0x18]
10002ee7c:     	stur	w27, [x8, #-0x38]
10002ee80:     	b	0x10002e574 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc4>
10002ee84:     	ldr	w0, [x27, #0x18]
10002ee88:     	cmp	x21, x0
10002ee8c:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002ee90:     	add	x8, x28, x0, lsl #4
10002ee94:     	mov	x0, x5
10002ee98:     	mov	x1, x4
10002ee9c:     	stp	x5, x4, [x8]
10002eea0:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002eea4:     	ldr	w0, [x27, #0x14]
10002eea8:     	cmn	w0, #0x1
10002eeac:     	b.eq	0x10002f5c8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1118>
10002eeb0:     	cmp	x21, x0
10002eeb4:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002eeb8:     	lsl	x8, x0, #4
10002eebc:     	ldr	x8, [x28, x8]
10002eec0:     	tst	x8, #0xffffffff
10002eec4:     	csel	x0, x7, x5, eq
10002eec8:     	csel	x1, x6, x4, eq
10002eecc:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002eed0:     	mov	w1, w9
10002eed4:     	mov	x2, x5
10002eed8:     	mov	w4, wzr
10002eedc:     	mov	x5, x7
10002eee0:     	b	0x10002f5b4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1104>
10002eee4:     	cmp	x5, #0x0
10002eee8:     	mov	x1, xzr
10002eeec:     	cset	w0, eq
10002eef0:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002eef4:     	cmp	x5, x7
10002eef8:     	mov	x1, xzr
10002eefc:     	cset	w0, eq
10002ef00:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef04:     	cmp	w9, #0x9
10002ef08:     	b.ne	0x10002f5d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1124>
10002ef0c:     	cmp	x5, #0x0
10002ef10:     	cset	w8, ne
10002ef14:     	b	0x10002f5dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ef18:     	sxtw	x0, w5
10002ef1c:     	mov	x1, xzr
10002ef20:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef24:     	mov	x1, xzr
10002ef28:     	mov	w0, w5
10002ef2c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef30:     	mov	x1, x2
10002ef34:     	mov	x2, x8
10002ef38:     	mov	x3, x5
10002ef3c:     	mov	x4, x6
10002ef40:     	mov	x5, x10
10002ef44:     	mov	x6, x11
10002ef48:     	bl	0x100052ba8 <_github.com/cpunion/wasmvm/internal/engine/simd.Eval>
10002ef4c:     	ldr	x15, [sp, #0x28]
10002ef50:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef54:     	cmp	w5, #0x0
10002ef58:     	cset	w8, eq
10002ef5c:     	b	0x10002f5dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ef60:     	ldr	x8, [sp, #0x20]
10002ef64:     	ldr	w0, [x27, #0x18]
10002ef68:     	ldr	x1, [x8, #0x48]
10002ef6c:     	cmp	x1, x0
10002ef70:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ef74:     	ldr	x8, [x8, #0x40]
10002ef78:     	mov	x1, xzr
10002ef7c:     	ldr	x0, [x8, x0, lsl #3]
10002ef80:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef84:     	ldr	x8, [sp, #0x20]
10002ef88:     	ldr	w0, [x27, #0x18]
10002ef8c:     	ldr	x1, [x8, #0x60]
10002ef90:     	cmp	x1, x0
10002ef94:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ef98:     	ldr	x8, [x8, #0x58]
10002ef9c:     	adds	x8, x8, x0, lsl #3
10002efa0:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002efa4:     	ldr	x8, [x8]
10002efa8:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002efac:     	ldrb	w9, [x8, #0x29]
10002efb0:     	mov	w10, w5
10002efb4:     	ldr	x11, [x8, #0x40]
10002efb8:     	cmp	w9, #0x0
10002efbc:     	csel	x9, x5, x10, ne
10002efc0:     	cmp	x9, x11
10002efc4:     	b.hs	0x10002facc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x161c>
10002efc8:     	ldr	x8, [x8, #0x38]
10002efcc:     	mov	x0, xzr
10002efd0:     	mov	x1, xzr
10002efd4:     	str	x7, [x8, x9, lsl #3]
10002efd8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002efdc:     	ldr	x8, [sp, #0x20]
10002efe0:     	ldr	w0, [x27, #0x18]
10002efe4:     	ldr	x1, [x8, #0x60]
10002efe8:     	cmp	x1, x0
10002efec:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002eff0:     	ldr	x8, [x8, #0x58]
10002eff4:     	adds	x8, x8, x0, lsl #3
10002eff8:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002effc:     	ldr	x8, [x8]
10002f000:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f004:     	ldrb	w9, [x8, #0x29]
10002f008:     	mov	w10, w5
10002f00c:     	ldr	x11, [x8, #0x40]
10002f010:     	cmp	w9, #0x0
10002f014:     	csel	x9, x5, x10, ne
10002f018:     	cmp	x9, x11
10002f01c:     	b.hs	0x10002facc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x161c>
10002f020:     	ldr	x8, [x8, #0x38]
10002f024:     	mov	x1, xzr
10002f028:     	ldr	x0, [x8, x9, lsl #3]
10002f02c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f030:     	ldr	x8, [sp, #0x20]
10002f034:     	ldr	w0, [x27, #0x18]
10002f038:     	ldr	x1, [x8, #0x90]
10002f03c:     	cmp	x1, x0
10002f040:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f044:     	ldr	x8, [x8, #0x88]
10002f048:     	ldr	x8, [x8, x0, lsl #3]
10002f04c:     	ldp	x0, x1, [x8, #0x20]
10002f050:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f054:     	ldr	w0, [x27, #0x18]
10002f058:     	mov	x1, xzr
10002f05c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f060:     	ldr	x8, [sp, #0x20]
10002f064:     	ldr	w0, [x27, #0x18]
10002f068:     	ldr	x1, [x8, #0x78]
10002f06c:     	cmp	x1, x0
10002f070:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f074:     	ldr	x8, [x8, #0x70]
10002f078:     	adds	x8, x8, x0, lsl #3
10002f07c:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f080:     	cbz	x20, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f084:     	ldr	x25, [x8]
10002f088:     	cbz	x25, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f08c:     	ldr	x8, [x25, #0x10]
10002f090:     	ldr	x0, [x20]
10002f094:     	mov	w9, w5
10002f098:     	mov	x1, x25
10002f09c:     	ldrb	w8, [x8, #0x31]
10002f0a0:     	tst	w8, #0x1
10002f0a4:     	strb	w8, [sp, #0x5e]
10002f0a8:     	csel	x2, x5, x9, ne
10002f0ac:     	bl	0x100046da0 <_github.com/cpunion/wasmvm/internal/engine/state.(*Store).GrowMemory>
10002f0b0:     	tbz	w1, #0x0, 0x10002f7f0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1340>
10002f0b4:     	ldr	x15, [sp, #0x28]
10002f0b8:     	mov	x1, xzr
10002f0bc:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0c0:     	mov	x0, x5
10002f0c4:     	mov	x1, x4
10002f0c8:     	cbnz	x5, 0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0cc:     	b	0x10002fadc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x162c>
10002f0d0:     	ldr	x9, [sp, #0x20]
10002f0d4:     	ldr	w8, [x27, #0x18]
10002f0d8:     	ldr	x1, [x9, #0x90]
10002f0dc:     	cmp	x1, x8
10002f0e0:     	b.ls	0x10002fbb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002f0e4:     	ldr	x9, [x9, #0x88]
10002f0e8:     	mov	x0, xzr
10002f0ec:     	mov	x1, xzr
10002f0f0:     	ldr	x8, [x9, x8, lsl #3]
10002f0f4:     	stp	x5, x4, [x8, #0x20]
10002f0f8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0fc:     	ldr	w0, [x27, #0x18]
10002f100:     	cmp	x21, x0
10002f104:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002f108:     	add	x8, x28, x0, lsl #4
10002f10c:     	ldp	x0, x1, [x8]
10002f110:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f114:     	ldr	x8, [sp, #0x20]
10002f118:     	ldr	w0, [x27, #0x18]
10002f11c:     	ldr	x1, [x8, #0x78]
10002f120:     	cmp	x1, x0
10002f124:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f128:     	ldr	x8, [x8, #0x70]
10002f12c:     	adds	x8, x8, x0, lsl #3
10002f130:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f134:     	ldr	x8, [x8]
10002f138:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f13c:     	ldr	x0, [x8, #0x10]
10002f140:     	ldr	x9, [x0, #0x40]
10002f144:     	ldr	x8, [x0, #0x60]
10002f148:     	cbz	x9, 0x10002f834 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1384>
10002f14c:     	cbnz	x8, 0x10002f838 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1388>
10002f150:     	bl	0x100049a30 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).pagesLocked>
10002f154:     	ldr	x15, [sp, #0x28]
10002f158:     	mov	x1, xzr
10002f15c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f160:     	mov	x1, xzr
10002f164:     	eor	x0, x7, x5
10002f168:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f16c:     	lsr	x0, x5, x7
10002f170:     	mov	x1, xzr
10002f174:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f178:     	lsr	w0, w5, w7
10002f17c:     	mov	x1, xzr
10002f180:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f184:     	mov	x1, xzr
10002f188:     	orr	x0, x7, x5
10002f18c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f190:     	asr	w0, w5, w7
10002f194:     	mov	x1, xzr
10002f198:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f19c:     	mov	x1, xzr
10002f1a0:     	eor	w0, w7, w5
10002f1a4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f1a8:     	lsr	x8, x5, #1
10002f1ac:     	and	x9, x5, #0x5555555555555555
10002f1b0:     	mov	x1, xzr
10002f1b4:     	and	x8, x8, #0x5555555555555555
10002f1b8:     	add	x8, x8, x9
10002f1bc:     	lsr	x9, x8, #2
10002f1c0:     	and	x8, x8, #0x3333333333333333
10002f1c4:     	and	x9, x9, #0x3333333333333333
10002f1c8:     	add	x8, x9, x8
10002f1cc:     	add	x8, x8, x8, lsr #4
10002f1d0:     	and	x8, x8, #0xf0f0f0f0f0f0f0f
10002f1d4:     	add	x8, x8, x8, lsr #8
10002f1d8:     	add	x8, x8, x8, lsr #16
10002f1dc:     	add	x8, x8, x8, lsr #32
10002f1e0:     	and	x0, x8, #0x7f
10002f1e4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f1e8:     	cmp	x5, x7
10002f1ec:     	mov	x1, xzr
10002f1f0:     	cset	w0, lo
10002f1f4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f1f8:     	cmp	x5, x7
10002f1fc:     	mov	x1, xzr
10002f200:     	cset	w0, ls
10002f204:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f208:     	cmp	x5, x7
10002f20c:     	mov	x1, xzr
10002f210:     	cset	w0, hi
10002f214:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f218:     	mul	w0, w7, w5
10002f21c:     	mov	x1, xzr
10002f220:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f224:     	lsr	w8, w5, #16
10002f228:     	mov	x10, #-0x10             ; =-16
10002f22c:     	mov	w11, #0x20              ; =32
10002f230:     	mov	w12, #0x18              ; =24
10002f234:     	mov	x1, xzr
10002f238:     	cmp	w8, #0x0
10002f23c:     	csel	w8, w8, w5, ne
10002f240:     	csel	x10, x10, xzr, ne
10002f244:     	lsr	w9, w8, #8
10002f248:     	cmp	w8, #0xff
10002f24c:     	csel	w8, w9, w8, hi
10002f250:     	adrp	x9, 0x1002be000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x14c8>
10002f254:     	add	x9, x9, #0x1ed
10002f258:     	and	x8, x8, #0xff
10002f25c:     	ldrb	w8, [x9, x8]
10002f260:     	csel	x9, x12, x11, hi
10002f264:     	add	x9, x9, x10
10002f268:     	sub	x0, x9, x8
10002f26c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f270:     	neg	w8, w7
10002f274:     	mov	x1, xzr
10002f278:     	ror	w0, w5, w8
10002f27c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f280:     	cmp	x5, x7
10002f284:     	mov	x1, xzr
10002f288:     	cset	w0, gt
10002f28c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f290:     	cmp	w5, w7
10002f294:     	mov	x1, xzr
10002f298:     	cset	w0, lt
10002f29c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2a0:     	cmp	x5, x7
10002f2a4:     	mov	x1, xzr
10002f2a8:     	cset	w0, ne
10002f2ac:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2b0:     	cbz	w5, 0x10002f8dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x142c>
10002f2b4:     	neg	w8, w5
10002f2b8:     	mov	w9, #0xb531             ; =46385
10002f2bc:     	mov	x1, xzr
10002f2c0:     	and	w8, w5, w8
10002f2c4:     	movk	w9, #0x77c, lsl #16
10002f2c8:     	mul	w8, w8, w9
10002f2cc:     	adrp	x9, 0x1002be000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x14c8>
10002f2d0:     	add	x9, x9, #0x1cd
10002f2d4:     	lsr	w8, w8, #27
10002f2d8:     	ldrb	w0, [x9, w8, uxtw]
10002f2dc:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2e0:     	mov	x1, xzr
10002f2e4:     	add	w0, w7, w5
10002f2e8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2ec:     	cmp	w5, w7
10002f2f0:     	mov	x1, xzr
10002f2f4:     	cset	w0, ne
10002f2f8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2fc:     	cmp	x5, x7
10002f300:     	mov	x1, xzr
10002f304:     	cset	w0, le
10002f308:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f30c:     	ror	w0, w5, w7
10002f310:     	mov	x1, xzr
10002f314:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f318:     	cmp	w5, w7
10002f31c:     	mov	x1, xzr
10002f320:     	cset	w0, ls
10002f324:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f328:     	cmp	w5, w7
10002f32c:     	mov	x1, xzr
10002f330:     	cset	w0, ge
10002f334:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f338:     	cmp	w5, #0x0
10002f33c:     	mov	x1, xzr
10002f340:     	cset	w0, eq
10002f344:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f348:     	cmp	w5, w7
10002f34c:     	mov	x1, xzr
10002f350:     	cset	w0, lo
10002f354:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f358:     	lsr	w8, w5, #24
10002f35c:     	ubfx	x10, x5, #16, #8
10002f360:     	ubfx	x11, x5, #8, #8
10002f364:     	and	x9, x5, #0xff
10002f368:     	adrp	x12, 0x1002a3000 <dyld_stub_binder+0x1002a3000>
10002f36c:     	add	x12, x12, #0x7ac
10002f370:     	ldrb	w8, [x12, x8]
10002f374:     	ldrb	w10, [x12, x10]
10002f378:     	ldrb	w11, [x12, x11]
10002f37c:     	ldrb	w9, [x12, x9]
10002f380:     	mov	x1, xzr
10002f384:     	add	w8, w10, w8
10002f388:     	add	w9, w11, w9
10002f38c:     	add	w8, w8, w9
10002f390:     	and	x0, x8, #0xff
10002f394:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f398:     	mov	x1, xzr
10002f39c:     	add	x0, x7, x5
10002f3a0:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3a4:     	lsl	w0, w5, w7
10002f3a8:     	mov	x1, xzr
10002f3ac:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3b0:     	mov	x1, xzr
10002f3b4:     	and	x0, x7, x5
10002f3b8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3bc:     	mov	x1, xzr
10002f3c0:     	sub	x0, x5, x7
10002f3c4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3c8:     	cmp	w5, w7
10002f3cc:     	mov	x1, xzr
10002f3d0:     	cset	w0, eq
10002f3d4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3d8:     	neg	w8, w7
10002f3dc:     	mov	x1, xzr
10002f3e0:     	ror	x0, x5, x8
10002f3e4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3e8:     	cmp	w5, w7
10002f3ec:     	mov	x1, xzr
10002f3f0:     	cset	w0, hs
10002f3f4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3f8:     	mov	x1, xzr
10002f3fc:     	and	w0, w5, w7
10002f400:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f404:     	cmp	w5, w7
10002f408:     	mov	x1, xzr
10002f40c:     	cset	w0, le
10002f410:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f414:     	cmp	w5, w7
10002f418:     	mov	x1, xzr
10002f41c:     	cset	w0, gt
10002f420:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f424:     	lsr	x8, x5, #32
10002f428:     	mov	w10, #0x20              ; =32
10002f42c:     	mov	x1, xzr
10002f430:     	cmp	x8, #0x0
10002f434:     	csel	x8, x8, x5, ne
10002f438:     	csel	x10, x10, xzr, ne
10002f43c:     	lsr	x9, x8, #16
10002f440:     	cmp	x9, #0x0
10002f444:     	csel	x8, x9, x8, ne
10002f448:     	orr	x9, x10, #0x10
10002f44c:     	lsr	x11, x8, #8
10002f450:     	csel	x9, x9, x10, ne
10002f454:     	cmp	x8, #0xff
10002f458:     	orr	x10, x9, #0x8
10002f45c:     	csel	x8, x11, x8, hi
10002f460:     	adrp	x11, 0x1002be000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x14c8>
10002f464:     	add	x11, x11, #0x1ed
10002f468:     	and	x8, x8, #0xff
10002f46c:     	csel	x9, x10, x9, hi
10002f470:     	mov	w10, #0x40              ; =64
10002f474:     	ldrb	w8, [x11, x8]
10002f478:     	sub	x9, x10, x9
10002f47c:     	sub	x0, x9, x8
10002f480:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f484:     	cmp	x5, x7
10002f488:     	mov	x1, xzr
10002f48c:     	cset	w0, lt
10002f490:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f494:     	ror	x0, x5, x7
10002f498:     	mov	x1, xzr
10002f49c:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4a0:     	cmp	w5, w7
10002f4a4:     	mov	x1, xzr
10002f4a8:     	cset	w0, hi
10002f4ac:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4b0:     	sxtb	x0, w5
10002f4b4:     	mov	x1, xzr
10002f4b8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4bc:     	sxth	x0, w5
10002f4c0:     	mov	x1, xzr
10002f4c4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4c8:     	cmp	x5, x7
10002f4cc:     	mov	x1, xzr
10002f4d0:     	cset	w0, hs
10002f4d4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4d8:     	cmp	x5, x7
10002f4dc:     	mov	x1, xzr
10002f4e0:     	cset	w0, ge
10002f4e4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4e8:     	asr	x0, x5, x7
10002f4ec:     	mov	x1, xzr
10002f4f0:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4f4:     	mov	x1, xzr
10002f4f8:     	orr	w0, w7, w5
10002f4fc:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f500:     	mov	x1, xzr
10002f504:     	sub	w0, w5, w7
10002f508:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f50c:     	mul	x0, x7, x5
10002f510:     	mov	x1, xzr
10002f514:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f518:     	sxtb	x8, w5
10002f51c:     	mov	x1, xzr
10002f520:     	mov	w0, w8
10002f524:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f528:     	rbit	x8, x5
10002f52c:     	mov	x1, xzr
10002f530:     	clz	x0, x8
10002f534:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f538:     	lsl	x0, x5, x7
10002f53c:     	mov	x1, xzr
10002f540:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f544:     	sxth	x8, w5
10002f548:     	mov	x1, xzr
10002f54c:     	mov	w0, w8
10002f550:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f554:     	sub	w8, w9, #0x8b
10002f558:     	cmp	w8, #0x34
10002f55c:     	b.hi	0x10002f57c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x10cc>
10002f560:     	mov	w10, #0x1               ; =1
10002f564:     	lsl	x8, x10, x8
10002f568:     	mov	x10, #-0x10000001       ; =-268435457
10002f56c:     	movk	x10, #0xfff9, lsl #32
10002f570:     	movk	x10, #0x1f, lsl #48
10002f574:     	tst	x8, x10
10002f578:     	b.ne	0x10002e964 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b4>
10002f57c:     	sub	w8, w9, #0x61
10002f580:     	mov	x0, xzr
10002f584:     	cmp	w8, #0x21
10002f588:     	b.hi	0x10002f994 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14e4>
10002f58c:     	mov	w10, #0x1               ; =1
10002f590:     	mov	x1, x0
10002f594:     	lsl	x8, x10, x8
10002f598:     	mov	x10, #0x3c0000000       ; =16106127360
10002f59c:     	movk	x10, #0xf03f
10002f5a0:     	tst	x8, x10
10002f5a4:     	b.ne	0x10002e964 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b4>
10002f5a8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f5ac:     	mov	x0, x8
10002f5b0:     	mov	w1, w9
10002f5b4:     	bl	0x10004a410 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).storeLocked>
10002f5b8:     	mov	w25, w0
10002f5bc:     	mov	x0, xzr
10002f5c0:     	mov	x1, xzr
10002f5c4:     	b	0x10002edf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002f5c8:     	mov	x0, x7
10002f5cc:     	mov	x1, x6
10002f5d0:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f5d4:     	cmp	w9, #0x5
10002f5d8:     	b.eq	0x10002fad4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1624>
10002f5dc:     	ldr	w9, [x27, #0x18]
10002f5e0:     	ldr	x1, [x15, #0xe8]
10002f5e4:     	add	w0, w9, w8
10002f5e8:     	cmp	x1, x0
10002f5ec:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f5f0:     	ldr	x8, [x15, #0xe0]
10002f5f4:     	mov	w9, #0xc                ; =12
10002f5f8:     	umaddl	x8, w0, w9, x8
10002f5fc:     	ldp	w27, w25, [x8]
10002f600:     	ldr	w10, [x8, #0x8]
10002f604:     	cmp	w27, w24
10002f608:     	b.hi	0x10002f870 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f60c:     	ldr	x8, [x20, #0x160]
10002f610:     	cbz	x8, 0x10002f620 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1170>
10002f614:     	cbz	x20, 0x10002f66c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11bc>
10002f618:     	ldar	w8, [x8]
10002f61c:     	cbz	w8, 0x10002f85c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13ac>
10002f620:     	ldr	x8, [x20, #0x8]
10002f624:     	cbz	x8, 0x10002fc4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x179c>
10002f628:     	ldr	x9, [x8, #0x8]
10002f62c:     	ldr	x0, [x20, #0x10]
10002f630:     	ldrb	w9, [x9, #0x17]
10002f634:     	tbz	w9, #0x5, 0x10002f814 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1364>
10002f638:     	and	w9, w9, #0x1f
10002f63c:     	mov	w24, w10
10002f640:     	cmp	w9, #0x16
10002f644:     	b.eq	0x10002f650 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11a0>
10002f648:     	stur	x0, [x29, #-0x78]
10002f64c:     	sub	x0, x29, #0x78
10002f650:     	ldr	x8, [x8, #0x28]
10002f654:     	blr	x8
10002f658:     	ldr	x15, [sp, #0x28]
10002f65c:     	mov	x2, x1
10002f660:     	mov	w10, w24
10002f664:     	cbnz	x0, 0x10002f868 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13b8>
10002f668:     	b	0x10002f870 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f66c:     	mov	w0, #0x1                ; =1
10002f670:     	mov	w24, w10
10002f674:     	bl	0x10021aef4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002f678:     	ldr	x15, [sp, #0x28]
10002f67c:     	mov	w10, w24
10002f680:     	cbnz	x0, 0x10002f868 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13b8>
10002f684:     	b	0x10002f870 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f688:     	cmp	w8, #0x3
10002f68c:     	b.eq	0x10002f9ec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x153c>
10002f690:     	cmp	w8, #0x6
10002f694:     	b.eq	0x10002f99c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14ec>
10002f698:     	sub	w8, w9, #0x12
10002f69c:     	cmp	w8, #0x4
10002f6a0:     	b.lo	0x10002f9c4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1514>
10002f6a4:     	ldr	w0, [x27, #0x14]
10002f6a8:     	cmn	w0, #0x1
10002f6ac:     	b.eq	0x10002f6fc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x124c>
10002f6b0:     	cmp	x21, x0
10002f6b4:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002f6b8:     	add	x8, x28, x0, lsl #4
10002f6bc:     	ldp	x10, x11, [x8]
10002f6c0:     	and	w8, w9, #0xffff0000
10002f6c4:     	cmp	w8, #0xfe0, lsl #12     ; =0xfe0000
10002f6c8:     	b.ne	0x10002f710 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1260>
10002f6cc:     	and	w4, w9, #0xffff
10002f6d0:     	cmp	w4, #0x3
10002f6d4:     	b.ne	0x10002f8e8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1438>
10002f6d8:     	cbz	x20, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f6dc:     	ldr	x8, [x20]
10002f6e0:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f6e4:     	ldr	x0, [x8, #0x8]
10002f6e8:     	bl	0x100049038 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Domain).Fence>
10002f6ec:     	ldr	x15, [sp, #0x28]
10002f6f0:     	mov	x0, xzr
10002f6f4:     	mov	x1, xzr
10002f6f8:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f6fc:     	mov	x10, xzr
10002f700:     	mov	x11, xzr
10002f704:     	and	w8, w9, #0xffff0000
10002f708:     	cmp	w8, #0xfe0, lsl #12     ; =0xfe0000
10002f70c:     	b.eq	0x10002f6cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x121c>
10002f710:     	mov	w8, #0xffed             ; =65517
10002f714:     	movk	w8, #0xff03, lsl #16
10002f718:     	add	w8, w8, w9
10002f71c:     	add	w8, w8, #0xb
10002f720:     	cmp	w8, #0xa
10002f724:     	b.hs	0x10002fc7c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17cc>
10002f728:     	ldr	x1, [x15, #0x80]
10002f72c:     	ldr	w0, [x27, #0x4]
10002f730:     	cmp	x1, x0
10002f734:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f738:     	ldr	x8, [x15, #0x78]
10002f73c:     	mov	w9, #0xe0               ; =224
10002f740:     	umaddl	x8, w0, w9, x8
10002f744:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f748:     	ldr	w9, [x8]
10002f74c:     	ldr	x12, [x8, #0x8]
10002f750:     	add	x2, sp, #0x88
10002f754:     	ldr	q0, [x8, #0x10]
10002f758:     	ldur	q1, [x8, #0x48]
10002f75c:     	mov	x0, x20
10002f760:     	str	w9, [sp, #0x88]
10002f764:     	ldur	q2, [x8, #0x68]
10002f768:     	mov	x3, x5
10002f76c:     	str	x12, [sp, #0x90]
10002f770:     	ldp	x9, x12, [x8, #0x20]
10002f774:     	stur	q0, [sp, #0x98]
10002f778:     	mov	x5, x7
10002f77c:     	mov	x7, x10
10002f780:     	ldr	q0, [x8, #0x30]
10002f784:     	ldr	x1, [sp, #0x20]
10002f788:     	stp	x9, x12, [sp, #0xa8]
10002f78c:     	add	x12, sp, #0x88
10002f790:     	ldrb	w9, [x8, #0x40]
10002f794:     	str	q0, [x12, #0x30]
10002f798:     	ldur	q0, [x8, #0x58]
10002f79c:     	strb	w9, [sp, #0xc8]
10002f7a0:     	stur	q1, [x12, #0x48]
10002f7a4:     	ldur	q1, [x8, #0x78]
10002f7a8:     	stur	q0, [x12, #0x58]
10002f7ac:     	ldur	q0, [x8, #0x88]
10002f7b0:     	ldp	x9, x8, [x8, #0x98]
10002f7b4:     	stur	q2, [x12, #0x68]
10002f7b8:     	stur	q1, [x12, #0x78]
10002f7bc:     	stur	q0, [x12, #0x88]
10002f7c0:     	stp	x9, x8, [sp, #0x120]
10002f7c4:     	add	x8, sp, #0x60
10002f7c8:     	str	x11, [sp]
10002f7cc:     	bl	0x100033d00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).bulk>
10002f7d0:     	ldp	x0, x1, [sp, #0x60]
10002f7d4:     	ldr	x8, [sp, #0x78]
10002f7d8:     	ldrb	w25, [sp, #0x70]
10002f7dc:     	cbz	x8, 0x10002edf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002f7e0:     	ldr	x9, [x8, #0x8]
10002f7e4:     	ldr	x15, [sp, #0x28]
10002f7e8:     	cbz	x9, 0x10002edf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x948>
10002f7ec:     	b	0x10002fb70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16c0>
10002f7f0:     	ldr	x8, [x25, #0x10]
10002f7f4:     	ldr	x15, [sp, #0x28]
10002f7f8:     	mov	x1, xzr
10002f7fc:     	ldrb	w8, [x8, #0x31]
10002f800:     	tst	w8, #0x1
10002f804:     	strb	w8, [sp, #0x5f]
10002f808:     	mov	w8, #-0x1               ; =-1
10002f80c:     	csinv	x0, x8, xzr, eq
10002f810:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f814:     	mov	w24, w10
10002f818:     	ldr	x8, [x8, #0x28]
10002f81c:     	blr	x8
10002f820:     	ldr	x15, [sp, #0x28]
10002f824:     	mov	x2, x1
10002f828:     	mov	w10, w24
10002f82c:     	cbnz	x0, 0x10002f868 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13b8>
10002f830:     	b	0x10002f870 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f834:     	cbz	x8, 0x10002f978 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14c8>
10002f838:     	ldar	x8, [x8]
10002f83c:     	cbz	x0, 0x10002f980 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14d0>
10002f840:     	ldr	x9, [x0, #0x38]
10002f844:     	mov	w10, #0x10000           ; =65536
10002f848:     	mov	x1, xzr
10002f84c:     	cmp	x9, #0x0
10002f850:     	csel	x9, x10, x9, eq
10002f854:     	udiv	x0, x8, x9
10002f858:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f85c:     	mov	x0, xzr
10002f860:     	mov	x2, xzr
10002f864:     	b	0x10002f870 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f868:     	ldr	x8, [x0, #0x8]
10002f86c:     	cbnz	x8, 0x10002fa64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15b4>
10002f870:     	ldr	x8, [x15, #0x108]
10002f874:     	add	w2, w10, w25
10002f878:     	cmp	x8, x2
10002f87c:     	b.lo	0x10002fbd0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1720>
10002f880:     	cmp	w25, w2
10002f884:     	b.hi	0x10002fc3c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x178c>
10002f888:     	sub	x8, x8, x25
10002f88c:     	lsl	x9, x25, #3
10002f890:     	cmp	x8, #0x0
10002f894:     	csel	x8, x9, xzr, gt
10002f898:     	cbz	w10, 0x10002e574 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc4>
10002f89c:     	ldr	x9, [x15, #0xf8]
10002f8a0:     	add	x8, x9, x8
10002f8a4:     	sub	x9, x2, x25
10002f8a8:     	add	x10, x8, #0x4
10002f8ac:     	ldr	w0, [x10]
10002f8b0:     	cmp	x21, x0
10002f8b4:     	b.ls	0x10002fb94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002f8b8:     	ldur	w8, [x10, #-0x4]
10002f8bc:     	cmp	x21, x8
10002f8c0:     	b.ls	0x10002fba8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f8>
10002f8c4:     	ldr	q0, [x28, x0, lsl #4]
10002f8c8:     	subs	x9, x9, #0x1
10002f8cc:     	add	x10, x10, #0x8
10002f8d0:     	str	q0, [x28, x8, lsl #4]
10002f8d4:     	b.ne	0x10002f8ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13fc>
10002f8d8:     	b	0x10002e574 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc4>
10002f8dc:     	mov	x1, xzr
10002f8e0:     	mov	w0, #0x20               ; =32
10002f8e4:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f8e8:     	ldr	x1, [x15, #0x80]
10002f8ec:     	ldr	w0, [x27, #0x4]
10002f8f0:     	cmp	x1, x0
10002f8f4:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f8f8:     	ldr	x8, [x15, #0x78]
10002f8fc:     	mov	w9, #0xe0               ; =224
10002f900:     	umaddl	x8, w0, w9, x8
10002f904:     	cbz	x8, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f908:     	ldr	x9, [sp, #0x20]
10002f90c:     	ldr	w0, [x8, #0x18]
10002f910:     	ldr	x1, [x9, #0x78]
10002f914:     	cmp	x1, x0
10002f918:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f91c:     	ldr	x9, [x9, #0x70]
10002f920:     	adds	x9, x9, x0, lsl #3
10002f924:     	b.eq	0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f928:     	ldr	x9, [x9]
10002f92c:     	cbz	x9, 0x10002fa0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f930:     	ldp	x11, x1, [x20]
10002f934:     	ldr	w12, [x8, #0x14]
10002f938:     	ldr	w8, [x8, #0x1c]
10002f93c:     	ldr	x2, [x20, #0x10]
10002f940:     	ldr	x0, [x9, #0x10]
10002f944:     	ldr	x3, [x11, #0x8]
10002f948:     	orr	x6, x12, x8, lsl #32
10002f94c:     	sub	x8, x29, #0xa0
10002f950:     	str	x10, [sp]
10002f954:     	bl	0x10004af04 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).Atomic>
10002f958:     	ldp	x0, x1, [x29, #-0xa0]
10002f95c:     	ldur	x8, [x29, #-0x88]
10002f960:     	ldurb	w25, [x29, #-0x90]
10002f964:     	cbz	x8, 0x10002edf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002f968:     	ldr	x9, [x8, #0x8]
10002f96c:     	ldr	x15, [sp, #0x28]
10002f970:     	cbz	x9, 0x10002edf8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x948>
10002f974:     	b	0x10002fb78 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16c8>
10002f978:     	ldr	x8, [x0, #0x8]
10002f97c:     	cbnz	x0, 0x10002f840 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1390>
10002f980:     	mov	w0, #0x1                ; =1
10002f984:     	bl	0x10021aef4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002f988:     	ldr	x15, [sp, #0x28]
10002f98c:     	mov	x1, xzr
10002f990:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f994:     	mov	x1, x0
10002f998:     	b	0x10002ee00 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f99c:     	ldr	x8, [sp, #0x18]
10002f9a0:     	mov	x0, x20
10002f9a4:     	stur	w24, [x8, #-0x38]
10002f9a8:     	bl	0x100031968 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runRuntime>
10002f9ac:     	tst	w0, #0xff
10002f9b0:     	b.ne	0x10002fa70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15c0>
10002f9b4:     	ldr	x8, [x20, #0x48]
10002f9b8:     	cmp	x8, #0x0
10002f9bc:     	b.gt	0x10002e4ec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x3c>
10002f9c0:     	b	0x10002fc64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17b4>
10002f9c4:     	ldr	x8, [sp, #0x18]
10002f9c8:     	mov	x0, x20
10002f9cc:     	stur	w24, [x8, #-0x38]
10002f9d0:     	bl	0x100030a54 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runCall>
10002f9d4:     	b	0x10002f9ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14fc>
10002f9d8:     	ldr	x8, [sp, #0x18]
10002f9dc:     	mov	x0, x20
10002f9e0:     	stur	w24, [x8, #-0x38]
10002f9e4:     	bl	0x100031570 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runThrow>
10002f9e8:     	b	0x10002f9ac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14fc>
10002f9ec:     	ldr	x8, [sp, #0x18]
10002f9f0:     	mov	x0, x20
10002f9f4:     	stur	w24, [x8, #-0x38]
10002f9f8:     	bl	0x10003068c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).returnFrame>
10002f9fc:     	and	w8, w0, #0xff
10002fa00:     	tbz	w8, #0x0, 0x10002f9b4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1504>
10002fa04:     	mov	w0, #0x1                ; =1
10002fa08:     	b	0x10002fb4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x169c>
10002fa0c:     	mov	w0, #0x1                ; =1
10002fa10:     	bl	0x10021aef4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002fa14:     	mov	x0, xzr
10002fa18:     	b	0x10002fb4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x169c>
10002fa1c:     	mov	w0, #0x28               ; =40
10002fa20:     	bl	0x10022fbb0 <dyld_stub_binder+0x10022fbb0>
10002fa24:     	cbz	x0, 0x10002fc94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e4>
10002fa28:     	mov	x21, x0
10002fa2c:     	mov	w0, #0x28               ; =40
10002fa30:     	bl	0x100216bdc <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002fa34:     	ldr	x8, [x25, #0x8]
10002fa38:     	adrp	x9, 0x1003cc000 <_github.com/xgo-dev/llgo/runtime/internal/runtime.zeroVal+0x2b>
10002fa3c:     	adrp	x10, 0x1002a3000 <dyld_stub_binder+0x1002a3000>
10002fa40:     	add	x10, x10, #0x742
10002fa44:     	ldr	q0, [x9, #0xde0]
10002fa48:     	adrp	x1, 0x1002fd000 <__llgo_github.com/cpunion/wasmvm/internal/compiler/ir.BlockID+0x40>
10002fa4c:     	add	x1, x1, #0x660
10002fa50:     	stp	x8, x10, [x21, #0x10]
10002fa54:     	mov	w8, #0xe                ; =14
10002fa58:     	str	q0, [x21]
10002fa5c:     	str	x8, [x21, #0x20]
10002fa60:     	b	0x10002fb3c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x168c>
10002fa64:     	mov	x1, x0
10002fa68:     	mov	x0, x20
10002fa6c:     	b	0x10002fb44 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1694>
10002fa70:     	and	x0, x0, #0xff
10002fa74:     	b	0x10002fb4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x169c>
10002fa78:     	ldr	x21, [sp, #0x48]
10002fa7c:     	mov	w0, #0x8                ; =8
10002fa80:     	bl	0x10022fbb0 <dyld_stub_binder+0x10022fbb0>
10002fa84:     	cbz	x0, 0x10002fc94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e4>
10002fa88:     	mov	x22, x0
10002fa8c:     	mov	w0, #0x8                ; =8
10002fa90:     	bl	0x100216bdc <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002fa94:     	adrp	x2, 0x1002e0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fa98:     	add	x2, x2, #0xa00
10002fa9c:     	mov	x0, x25
10002faa0:     	mov	x1, x21
10002faa4:     	mov	x3, x22
10002faa8:     	bl	0x1001f57cc <_errors.As>
10002faac:     	ldr	x9, [sp, #0x8]
10002fab0:     	tbz	w0, #0x0, 0x10002fac0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1610>
10002fab4:     	ldr	x8, [x22]
10002fab8:     	ldr	w9, [x9, #0x50]
10002fabc:     	str	w9, [x8, #0x4]
10002fac0:     	mov	x0, x20
10002fac4:     	mov	x1, x25
10002fac8:     	b	0x10002fb40 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1690>
10002facc:     	mov	w25, #0x3               ; =3
10002fad0:     	b	0x10002fae0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002fad4:     	mov	w25, #0x1               ; =1
10002fad8:     	b	0x10002fae0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002fadc:     	mov	w25, #0x9               ; =9
10002fae0:     	mov	w0, #0x10               ; =16
10002fae4:     	bl	0x10022fba4 <dyld_stub_binder+0x10022fba4>
10002fae8:     	cbz	x0, 0x10002fc94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e4>
10002faec:     	mov	x21, x0
10002faf0:     	mov	w0, #0x10               ; =16
10002faf4:     	bl	0x100216bdc <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002faf8:     	ldr	x9, [sp, #0x28]
10002fafc:     	stp	xzr, xzr, [x21]
10002fb00:     	ldr	w0, [x27, #0x4]
10002fb04:     	ldr	x1, [x9, #0x80]
10002fb08:     	cmp	x1, x0
10002fb0c:     	b.ls	0x10002fb9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002fb10:     	mov	w8, #0xe0               ; =224
10002fb14:     	ldr	x9, [x9, #0x78]
10002fb18:     	adrp	x1, 0x1002e0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fb1c:     	add	x1, x1, #0xd68
10002fb20:     	umaddl	x8, w0, w8, x9
10002fb24:     	ldr	x9, [sp, #0x8]
10002fb28:     	ldr	w9, [x9, #0x50]
10002fb2c:     	ldr	x8, [x8, #0x8]
10002fb30:     	strb	w25, [x21]
10002fb34:     	str	w9, [x21, #0x4]
10002fb38:     	str	x8, [x21, #0x8]
10002fb3c:     	mov	x0, x20
10002fb40:     	mov	x2, x21
10002fb44:     	bl	0x10002d728 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).fail>
10002fb48:     	mov	w0, #0x3                ; =3
10002fb4c:     	mov	x1, x19
10002fb50:     	ldp	x29, x30, [sp, #0x1d0]
10002fb54:     	ldp	x20, x19, [sp, #0x1c0]
10002fb58:     	ldp	x22, x21, [sp, #0x1b0]
10002fb5c:     	ldp	x24, x23, [sp, #0x1a0]
10002fb60:     	ldp	x26, x25, [sp, #0x190]
10002fb64:     	ldp	x28, x27, [sp, #0x180]
10002fb68:     	add	sp, sp, #0x1e0
10002fb6c:     	ret
10002fb70:     	ldr	x2, [sp, #0x80]
10002fb74:     	b	0x10002fb7c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16cc>
10002fb78:     	ldur	x2, [x29, #-0x80]
10002fb7c:     	mov	x0, x20
10002fb80:     	mov	x1, x8
10002fb84:     	b	0x10002fb44 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1694>
10002fb88:     	mov	x0, x24
10002fb8c:     	mov	x1, x22
10002fb90:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb94:     	mov	x1, x21
10002fb98:     	bl	0x100216cd4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fb9c:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fba0:     	mov	x1, x22
10002fba4:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fba8:     	mov	x0, x8
10002fbac:     	mov	x1, x21
10002fbb0:     	bl	0x100216cd4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fbb4:     	mov	x0, x8
10002fbb8:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fbbc:     	mov	x1, x21
10002fbc0:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fbc4:     	mov	x0, x8
10002fbc8:     	mov	x1, x21
10002fbcc:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fbd0:     	mov	x0, x2
10002fbd4:     	mov	w1, wzr
10002fbd8:     	mov	x2, x8
10002fbdc:     	mov	w3, #0x2                ; =2
10002fbe0:     	bl	0x1002179ec <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fbe4:     	mov	x0, x9
10002fbe8:     	mov	w1, #0x1                ; =1
10002fbec:     	mov	w3, #0x2                ; =2
10002fbf0:     	bl	0x1002179ec <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fbf4:     	mov	x0, x8
10002fbf8:     	mov	w1, #0x1                ; =1
10002fbfc:     	mov	x2, x9
10002fc00:     	mov	w3, #0x3                ; =3
10002fc04:     	bl	0x1002179ec <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fc08:     	mov	x0, x9
10002fc0c:     	mov	x1, x21
10002fc10:     	bl	0x10021824c <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fc14:     	mov	w0, #0x1                ; =1
10002fc18:     	bl	0x100216cd4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fc1c:     	mov	x0, xzr
10002fc20:     	mov	x1, xzr
10002fc24:     	bl	0x100216cd4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fc28:     	mov	w0, #0x3                ; =3
10002fc2c:     	bl	0x100216cd4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fc30:     	mov	w0, #0x2                ; =2
10002fc34:     	mov	w1, #0x2                ; =2
10002fc38:     	bl	0x100216cd4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fc3c:     	mov	x0, x25
10002fc40:     	mov	w1, wzr
10002fc44:     	mov	w3, #0x3                ; =3
10002fc48:     	bl	0x1002179ec <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fc4c:     	adrp	x0, 0x100354000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8$in+0x10>
10002fc50:     	add	x0, x0, #0x890
10002fc54:     	adrp	x1, 0x100354000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8$in+0x10>
10002fc58:     	add	x1, x1, #0xb10
10002fc5c:     	bl	0x1002169c8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fc60:     	brk	#0x1
10002fc64:     	adrp	x0, 0x10035d000 <_*_llgo_github.com/xgo-dev/llgo/runtime/abi.UncommonType+0xa8>
10002fc68:     	add	x0, x0, #0xfb8
10002fc6c:     	adrp	x1, 0x1002e0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fc70:     	add	x1, x1, #0xa50
10002fc74:     	bl	0x1002169c8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fc78:     	brk	#0x1
10002fc7c:     	adrp	x0, 0x10035d000 <_*_llgo_github.com/xgo-dev/llgo/runtime/abi.UncommonType+0xa8>
10002fc80:     	add	x0, x0, #0xfb8
10002fc84:     	adrp	x1, 0x1002e0000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fc88:     	add	x1, x1, #0xa60
10002fc8c:     	bl	0x1002169c8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fc90:     	brk	#0x1
10002fc94:     	adrp	x0, 0x10035d000 <_*_llgo_github.com/xgo-dev/llgo/runtime/abi.UncommonType+0xa8>
10002fc98:     	add	x0, x0, #0xfb8
10002fc9c:     	adrp	x1, 0x100354000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8$in+0x10>
10002fca0:     	add	x1, x1, #0x880
10002fca4:     	bl	0x1002169c8 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fca8:     	brk	#0x1
