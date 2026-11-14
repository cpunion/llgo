/private/tmp/llgo-issue-audit-results-20261113/new-performance-followups/darwin-symbol-final-native/bin/llgo-main-base:	file format mach-o arm64

Disassembly of section __TEXT,__text:

000000010002e418 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop>:
10002e418:     	sub	sp, sp, #0x1e0
10002e41c:     	stp	x28, x27, [sp, #0x180]
10002e420:     	stp	x26, x25, [sp, #0x190]
10002e424:     	stp	x24, x23, [sp, #0x1a0]
10002e428:     	stp	x22, x21, [sp, #0x1b0]
10002e42c:     	stp	x20, x19, [sp, #0x1c0]
10002e430:     	stp	x29, x30, [sp, #0x1d0]
10002e434:     	add	x29, sp, #0x1d0
10002e438:     	ldr	x8, [x0, #0x48]
10002e43c:     	cmp	x8, #0x1
10002e440:     	b.lt	0x10002fbcc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17b4>
10002e444:     	ldrb	w26, [x0, #0x20]
10002e448:     	mov	x20, x0
10002e44c:     	mov	x19, x1
10002e450:     	str	w26, [sp, #0x14]
10002e454:     	ldr	x9, [x20, #0x40]
10002e458:     	add	x8, x9, x8, lsl #6
10002e45c:     	ldur	x10, [x8, #-0x40]
10002e460:     	sturb	wzr, [x8, #-0x30]
10002e464:     	str	x8, [sp, #0x18]
10002e468:     	ldur	x8, [x8, #-0x28]
10002e46c:     	ldr	x2, [x20, #0x88]
10002e470:     	ldr	x15, [x10, #0x48]
10002e474:     	ldr	x21, [x15, #0x98]
10002e478:     	add	x9, x21, x8
10002e47c:     	cmp	x9, x2
10002e480:     	b.hi	0x10002fb4c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1734>
10002e484:     	cmp	x8, x9
10002e488:     	b.hi	0x10002fb5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1744>
10002e48c:     	ldr	x9, [sp, #0x18]
10002e490:     	ldr	x22, [x15, #0xb8]
10002e494:     	ldur	w0, [x9, #-0x38]
10002e498:     	sub	x9, x2, x8
10002e49c:     	lsl	x8, x8, #4
10002e4a0:     	cmp	x9, #0x0
10002e4a4:     	csel	x8, x8, xzr, gt
10002e4a8:     	cmp	x22, x0
10002e4ac:     	b.ls	0x10002fb08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f0>
10002e4b0:     	ldr	x9, [x20, #0x78]
10002e4b4:     	ldr	x23, [x15, #0xb0]
10002e4b8:     	mov	w24, w0
10002e4bc:     	str	x10, [sp, #0x8]
10002e4c0:     	ldr	x10, [x10, #0x40]
10002e4c4:     	add	x28, x9, x8
10002e4c8:     	stp	x10, x15, [sp, #0x20]
10002e4cc:     	b	0x10002e4ec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xd4>
10002e4d0:     	add	x8, x28, x8, lsl #4
10002e4d4:     	stp	x0, x1, [x8]
10002e4d8:     	add	w27, w24, #0x1
10002e4dc:     	mov	w0, w27
10002e4e0:     	mov	w24, w27
10002e4e4:     	cmp	x22, x0
10002e4e8:     	b.ls	0x10002fb08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f0>
10002e4ec:     	add	x27, x23, x0, lsl #5
10002e4f0:     	tbz	w26, #0x0, 0x10002e530 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x118>
10002e4f4:     	ldr	x1, [x15, #0x80]
10002e4f8:     	ldr	w0, [x27, #0x4]
10002e4fc:     	cmp	x1, x0
10002e500:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e504:     	mov	w9, #0xe0               ; =224
10002e508:     	ldr	x8, [x15, #0x78]
10002e50c:     	umaddl	x25, w0, w9, x8
10002e510:     	ldr	w8, [x25]
10002e514:     	cmp	w8, #0x5
10002e518:     	ccmp	w8, #0xb, #0x4, ne
10002e51c:     	ldr	x8, [x20, #0x150]
10002e520:     	cset	w9, ne
10002e524:     	subs	x8, x8, x9
10002e528:     	b.lo	0x10002f984 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x156c>
10002e52c:     	str	x8, [x20, #0x150]
10002e530:     	add	x19, x19, #0x1
10002e534:     	tst	x19, #0x3f
10002e538:     	b.ne	0x10002e5bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e53c:     	ldr	x8, [x20, #0x160]
10002e540:     	cbz	x8, 0x10002e550 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x138>
10002e544:     	cbz	x20, 0x10002e594 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17c>
10002e548:     	ldar	w8, [x8]
10002e54c:     	cbz	w8, 0x10002e5a8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x190>
10002e550:     	ldr	x8, [x20, #0x8]
10002e554:     	cbz	x8, 0x10002fbb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x179c>
10002e558:     	ldr	x9, [x8, #0x8]
10002e55c:     	ldr	x0, [x20, #0x10]
10002e560:     	ldrb	w9, [x9, #0x17]
10002e564:     	tbz	w9, #0x5, 0x10002e57c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x164>
10002e568:     	and	w9, w9, #0x1f
10002e56c:     	cmp	w9, #0x16
10002e570:     	b.eq	0x10002e57c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x164>
10002e574:     	stur	x0, [x29, #-0x78]
10002e578:     	sub	x0, x29, #0x78
10002e57c:     	ldr	x8, [x8, #0x28]
10002e580:     	blr	x8
10002e584:     	ldr	x15, [sp, #0x28]
10002e588:     	mov	x2, x1
10002e58c:     	cbnz	x0, 0x10002e5b4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x19c>
10002e590:     	b	0x10002e5bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e594:     	mov	w0, #0x1                ; =1
10002e598:     	bl	0x10021a0a4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002e59c:     	ldr	x15, [sp, #0x28]
10002e5a0:     	cbnz	x0, 0x10002e5b4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x19c>
10002e5a4:     	b	0x10002e5bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e5a8:     	mov	x0, xzr
10002e5ac:     	mov	x2, xzr
10002e5b0:     	b	0x10002e5bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1a4>
10002e5b4:     	ldr	x8, [x0, #0x8]
10002e5b8:     	cbnz	x8, 0x10002f9cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15b4>
10002e5bc:     	ldr	w10, [x27, #0x1c]
10002e5c0:     	cbz	w10, 0x10002e63c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e5c4:     	tbz	w26, #0x0, 0x10002e5e0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1c8>
10002e5c8:     	ldr	w9, [x27]
10002e5cc:     	cmp	w9, #0x20
10002e5d0:     	b.eq	0x10002e63c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e5d4:     	cmp	w9, #0x41
10002e5d8:     	b.eq	0x10002e63c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e5dc:     	b	0x10002e640 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x228>
10002e5e0:     	ldr	w9, [x27]
10002e5e4:     	cmp	w9, #0x20
10002e5e8:     	b.eq	0x10002e5f4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1dc>
10002e5ec:     	cmp	w9, #0x41
10002e5f0:     	b.ne	0x10002e640 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x228>
10002e5f4:     	mvn	w8, w19
10002e5f8:     	tst	x8, #0x3f
10002e5fc:     	b.eq	0x10002e63c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x224>
10002e600:     	cmp	w9, #0x41
10002e604:     	b.ne	0x10002e620 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x208>
10002e608:     	ldr	w0, [x27, #0x8]
10002e60c:     	cmp	x21, x0
10002e610:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e614:     	ldr	w8, [x27, #0x18]
10002e618:     	add	x9, x28, x0, lsl #4
10002e61c:     	stp	x8, xzr, [x9]
10002e620:     	add	w24, w24, #0x1
10002e624:     	cmp	x22, x24
10002e628:     	b.ls	0x10002faf0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16d8>
10002e62c:     	add	x27, x23, x24, lsl #5
10002e630:     	add	x19, x19, #0x1
10002e634:     	ldr	w10, [x27, #0x1c]
10002e638:     	cbnz	w10, 0x10002e5e0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1c8>
10002e63c:     	ldr	w9, [x27]
10002e640:     	mov	w11, #0xff20            ; =65312
10002e644:     	sub	w8, w9, #0xe0
10002e648:     	movk	w11, #0xff, lsl #16
10002e64c:     	cmp	w8, w11
10002e650:     	b.hs	0x10002e86c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x454>
10002e654:     	lsr	w8, w9, #16
10002e658:     	cmp	w8, #0xfb
10002e65c:     	b.eq	0x10002e7cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x3b4>
10002e660:     	cmp	w8, #0xfc
10002e664:     	b.eq	0x10002e6d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x2c0>
10002e668:     	cmp	w8, #0xfd
10002e66c:     	b.ne	0x10002e864 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x44c>
10002e670:     	ldr	w0, [x27, #0xc]
10002e674:     	cmn	w0, #0x1
10002e678:     	b.eq	0x10002ea94 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x67c>
10002e67c:     	cmp	x21, x0
10002e680:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e684:     	add	x8, x28, x0, lsl #4
10002e688:     	ldp	x2, x8, [x8]
10002e68c:     	ldr	w0, [x27, #0x10]
10002e690:     	cmn	w0, #0x1
10002e694:     	b.eq	0x10002eaa8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x690>
10002e698:     	cmp	x21, x0
10002e69c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e6a0:     	add	x10, x28, x0, lsl #4
10002e6a4:     	ldp	x5, x6, [x10]
10002e6a8:     	ldr	w0, [x27, #0x14]
10002e6ac:     	cmn	w0, #0x1
10002e6b0:     	b.eq	0x10002eabc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6a4>
10002e6b4:     	cmp	x21, x0
10002e6b8:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e6bc:     	add	x11, x28, x0, lsl #4
10002e6c0:     	ldp	x10, x11, [x11]
10002e6c4:     	ldr	x1, [x15, #0x80]
10002e6c8:     	ldr	w0, [x27, #0x4]
10002e6cc:     	cmp	x1, x0
10002e6d0:     	b.hi	0x10002ead4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6bc>
10002e6d4:     	b	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e6d8:     	mov	w8, #0xffed             ; =65517
10002e6dc:     	movk	w8, #0xff03, lsl #16
10002e6e0:     	add	w11, w9, w8
10002e6e4:     	cmp	w11, #0x4
10002e6e8:     	b.hs	0x10002e86c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x454>
10002e6ec:     	ldr	w0, [x27, #0x18]
10002e6f0:     	ldr	x8, [x15, #0xd8]
10002e6f4:     	add	w10, w10, #0x2
10002e6f8:     	add	w2, w10, w0
10002e6fc:     	cmp	x8, x2
10002e700:     	b.lo	0x10002fb38 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1720>
10002e704:     	cmp	w0, w2
10002e708:     	b.hi	0x10002fba8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1790>
10002e70c:     	sub	x8, x8, x0
10002e710:     	lsl	x12, x0, #2
10002e714:     	cmp	x8, #0x0
10002e718:     	csel	x8, x12, xzr, gt
10002e71c:     	cbz	w10, 0x10002fb84 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x176c>
10002e720:     	ldr	x10, [x15, #0xc8]
10002e724:     	add	x10, x10, x8
10002e728:     	ldr	w8, [x10]
10002e72c:     	cmp	x21, x8
10002e730:     	b.ls	0x10002fb2c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1714>
10002e734:     	sub	x1, x2, x0
10002e738:     	cmp	x1, #0x1
10002e73c:     	b.eq	0x10002fb7c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1764>
10002e740:     	ldr	w0, [x10, #0x4]
10002e744:     	cmp	x21, x0
10002e748:     	b.ls	0x10002fb24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170c>
10002e74c:     	lsl	x8, x8, #4
10002e750:     	lsl	x12, x0, #4
10002e754:     	cmp	w11, #0x2
10002e758:     	ldr	x14, [x28, x8]
10002e75c:     	ldr	x13, [x28, x12]
10002e760:     	b.hs	0x10002ebc4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7ac>
10002e764:     	cmp	x1, #0x3
10002e768:     	b.lo	0x10002fb98 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1780>
10002e76c:     	ldr	w0, [x10, #0x8]
10002e770:     	cmp	x21, x0
10002e774:     	b.ls	0x10002fb24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170c>
10002e778:     	cmp	x1, #0x3
10002e77c:     	b.eq	0x10002fb90 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1778>
10002e780:     	ldr	w8, [x10, #0xc]
10002e784:     	cmp	x21, x8
10002e788:     	b.ls	0x10002fb2c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1714>
10002e78c:     	lsl	x11, x0, #4
10002e790:     	lsl	x8, x8, #4
10002e794:     	ldr	x12, [x28, x11]
10002e798:     	mov	w11, #0x13              ; =19
10002e79c:     	ldr	x8, [x28, x8]
10002e7a0:     	movk	w11, #0xfc, lsl #16
10002e7a4:     	cmp	w9, w11
10002e7a8:     	b.ne	0x10002ed80 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x968>
10002e7ac:     	add	x11, x12, x14
10002e7b0:     	orr	x9, x12, x14
10002e7b4:     	and	x12, x12, x14
10002e7b8:     	bic	x9, x9, x11
10002e7bc:     	orr	x9, x9, x12
10002e7c0:     	add	x9, x13, x9, lsr #63
10002e7c4:     	add	x12, x9, x8
10002e7c8:     	b	0x10002ed9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x984>
10002e7cc:     	sub	w8, w9, #0x2
10002e7d0:     	and	w9, w8, #0xffff
10002e7d4:     	cmp	w9, #0x20
10002e7d8:     	b.hi	0x10002e9f8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5e0>
10002e7dc:     	mov	x9, #0x3e0f             ; =15887
10002e7e0:     	movk	x9, #0x1c00, lsl #16
10002e7e4:     	movk	x9, #0x1, lsl #32
10002e7e8:     	lsr	x9, x9, x8
10002e7ec:     	tbz	w9, #0x0, 0x10002e9f8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5e0>
10002e7f0:     	ldr	w0, [x27, #0xc]
10002e7f4:     	cmn	w0, #0x1
10002e7f8:     	b.eq	0x10002ec70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x858>
10002e7fc:     	cmp	x21, x0
10002e800:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e804:     	add	x9, x28, x0, lsl #4
10002e808:     	ldp	x3, x4, [x9]
10002e80c:     	ldr	w0, [x27, #0x10]
10002e810:     	cmn	w0, #0x1
10002e814:     	b.eq	0x10002ec84 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x86c>
10002e818:     	cmp	x21, x0
10002e81c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e820:     	add	x9, x28, x0, lsl #4
10002e824:     	ldp	x5, x6, [x9]
10002e828:     	and	w8, w8, #0xffff
10002e82c:     	cmp	w8, #0xc
10002e830:     	b.ne	0x10002ec98 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x880>
10002e834:     	ldr	w8, [x27, #0x18]
10002e838:     	ldr	x1, [x15, #0xd0]
10002e83c:     	add	w0, w8, #0x2
10002e840:     	cmp	x1, x0
10002e844:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e848:     	ldr	x8, [x15, #0xc8]
10002e84c:     	ldr	w0, [x8, x0, lsl #2]
10002e850:     	cmp	x21, x0
10002e854:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e858:     	add	x8, x28, x0, lsl #4
10002e85c:     	ldp	x7, x9, [x8]
10002e860:     	b	0x10002eca0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x888>
10002e864:     	cmp	w9, #0xe7
10002e868:     	b.lo	0x10002f904 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14ec>
10002e86c:     	ldr	w0, [x27, #0xc]
10002e870:     	cmn	w0, #0x1
10002e874:     	b.eq	0x10002e8f4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4dc>
10002e878:     	cmp	x21, x0
10002e87c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e880:     	add	x8, x28, x0, lsl #4
10002e884:     	ldp	x5, x4, [x8]
10002e888:     	ldr	w0, [x27, #0x10]
10002e88c:     	cmn	w0, #0x1
10002e890:     	b.eq	0x10002e908 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4f0>
10002e894:     	cmp	x21, x0
10002e898:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002e89c:     	add	x8, x28, x0, lsl #4
10002e8a0:     	ldp	x7, x6, [x8]
10002e8a4:     	sub	w8, w9, #0x45
10002e8a8:     	cmp	w8, #0x7f
10002e8ac:     	b.hi	0x10002e91c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x504>
10002e8b0:     	b.hi	0x10002f4bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x10a4>
10002e8b4:     	adrp	x12, 0x100231000 <dyld_stub_binder+0x100231000>
10002e8b8:     	add	x12, x12, #0x398
10002e8bc:     	adr	x10, 0x10002e8cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b4>
10002e8c0:     	ldrh	w11, [x12, x8, lsl #1]
10002e8c4:     	add	x10, x10, x11, lsl #2
10002e8c8:     	br	x10
10002e8cc:     	mov	w0, w9
10002e8d0:     	mov	x1, x5
10002e8d4:     	mov	x2, x7
10002e8d8:     	bl	0x100054620 <_github.com/cpunion/wasmvm/internal/engine/numeric.Eval>
10002e8dc:     	ldr	x15, [sp, #0x28]
10002e8e0:     	mov	x25, x1
10002e8e4:     	tst	w25, #0xff
10002e8e8:     	b.ne	0x10002fa48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002e8ec:     	mov	x1, xzr
10002e8f0:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002e8f4:     	mov	x5, xzr
10002e8f8:     	mov	x4, xzr
10002e8fc:     	ldr	w0, [x27, #0x10]
10002e900:     	cmn	w0, #0x1
10002e904:     	b.ne	0x10002e894 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x47c>
10002e908:     	mov	x7, xzr
10002e90c:     	mov	x6, xzr
10002e910:     	sub	w8, w9, #0x45
10002e914:     	cmp	w8, #0x7f
10002e918:     	b.ls	0x10002e8b0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x498>
10002e91c:     	sub	w8, w9, #0x1
10002e920:     	cmp	w8, #0xd3
10002e924:     	b.hi	0x10002e9cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x5b4>
10002e928:     	adrp	x13, 0x100231000 <dyld_stub_binder+0x100231000>
10002e92c:     	add	x13, x13, #0x1f0
10002e930:     	mov	x0, xzr
10002e934:     	adr	x11, 0x10002e948 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x530>
10002e938:     	ldrh	w12, [x13, x8, lsl #1]
10002e93c:     	add	x11, x11, x12, lsl #2
10002e940:     	mov	x1, xzr
10002e944:     	br	x11
10002e948:     	ldr	x1, [x15, #0x80]
10002e94c:     	ldr	w0, [x27, #0x4]
10002e950:     	cmp	x1, x0
10002e954:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e958:     	ldr	x8, [x15, #0x78]
10002e95c:     	mov	w10, #0xe0              ; =224
10002e960:     	umaddl	x8, w0, w10, x8
10002e964:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002e968:     	ldr	x10, [sp, #0x20]
10002e96c:     	ldr	w0, [x8, #0x18]
10002e970:     	ldr	x1, [x10, #0x78]
10002e974:     	cmp	x1, x0
10002e978:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002e97c:     	ldr	x10, [x10, #0x70]
10002e980:     	adds	x10, x10, x0, lsl #3
10002e984:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002e988:     	ldr	x10, [x10]
10002e98c:     	cbz	x10, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002e990:     	ldr	x0, [x10, #0x10]
10002e994:     	ldr	w10, [x8, #0x14]
10002e998:     	ldr	w8, [x8, #0x1c]
10002e99c:     	ldr	x11, [x0, #0x40]
10002e9a0:     	orr	x3, x10, x8, lsl #32
10002e9a4:     	cbz	x11, 0x10002e9b0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x598>
10002e9a8:     	ldr	x8, [x0, #0x60]
10002e9ac:     	cbz	x8, 0x10002ed30 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x918>
10002e9b0:     	sub	x8, x29, #0x78
10002e9b4:     	mov	w1, w9
10002e9b8:     	mov	x2, x5
10002e9bc:     	mov	w4, wzr
10002e9c0:     	mov	x5, xzr
10002e9c4:     	mov	x6, xzr
10002e9c8:     	b	0x10002eb64 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x74c>
10002e9cc:     	sub	w8, w9, #0xfc0, lsl #12 ; =0xfc0000
10002e9d0:     	cmp	w8, #0x8
10002e9d4:     	b.hs	0x10002eb70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x758>
10002e9d8:     	mov	w0, w9
10002e9dc:     	mov	x1, x5
10002e9e0:     	mov	x2, x7
10002e9e4:     	bl	0x100054620 <_github.com/cpunion/wasmvm/internal/engine/numeric.Eval>
10002e9e8:     	ldr	x15, [sp, #0x28]
10002e9ec:     	mov	x25, x1
10002e9f0:     	mov	x1, xzr
10002e9f4:     	b	0x10002ed60 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x948>
10002e9f8:     	ldr	x8, [sp, #0x18]
10002e9fc:     	mov	x0, x20
10002ea00:     	stur	w24, [x8, #-0x38]
10002ea04:     	bl	0x10002fe08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runGC>
10002ea08:     	ldr	x15, [sp, #0x28]
10002ea0c:     	tst	w0, #0xff
10002ea10:     	b.eq	0x10002ed08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8f0>
10002ea14:     	b	0x10002f9d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15c0>
10002ea18:     	ldr	x1, [x15, #0x80]
10002ea1c:     	ldr	w0, [x27, #0x4]
10002ea20:     	cmp	x1, x0
10002ea24:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ea28:     	ldr	x8, [x15, #0x78]
10002ea2c:     	mov	w10, #0xe0              ; =224
10002ea30:     	umaddl	x8, w0, w10, x8
10002ea34:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ea38:     	ldr	x10, [sp, #0x20]
10002ea3c:     	ldr	w0, [x8, #0x18]
10002ea40:     	ldr	x1, [x10, #0x78]
10002ea44:     	cmp	x1, x0
10002ea48:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ea4c:     	ldr	x10, [x10, #0x70]
10002ea50:     	adds	x10, x10, x0, lsl #3
10002ea54:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ea58:     	ldr	x10, [x10]
10002ea5c:     	cbz	x10, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ea60:     	ldr	x0, [x10, #0x10]
10002ea64:     	ldr	w10, [x8, #0x14]
10002ea68:     	ldr	w8, [x8, #0x1c]
10002ea6c:     	ldr	x11, [x0, #0x40]
10002ea70:     	orr	x3, x10, x8, lsl #32
10002ea74:     	cbz	x11, 0x10002ea80 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x668>
10002ea78:     	ldr	x8, [x0, #0x60]
10002ea7c:     	cbz	x8, 0x10002ee38 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa20>
10002ea80:     	mov	w1, w9
10002ea84:     	mov	x2, x5
10002ea88:     	mov	w4, wzr
10002ea8c:     	mov	x5, x7
10002ea90:     	b	0x10002ec58 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x840>
10002ea94:     	mov	x8, xzr
10002ea98:     	mov	x2, xzr
10002ea9c:     	ldr	w0, [x27, #0x10]
10002eaa0:     	cmn	w0, #0x1
10002eaa4:     	b.ne	0x10002e698 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x280>
10002eaa8:     	mov	x5, xzr
10002eaac:     	mov	x6, xzr
10002eab0:     	ldr	w0, [x27, #0x14]
10002eab4:     	cmn	w0, #0x1
10002eab8:     	b.ne	0x10002e6b4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x29c>
10002eabc:     	mov	x10, xzr
10002eac0:     	mov	x11, xzr
10002eac4:     	ldr	x1, [x15, #0x80]
10002eac8:     	ldr	w0, [x27, #0x4]
10002eacc:     	cmp	x1, x0
10002ead0:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ead4:     	mov	w13, #0xe0              ; =224
10002ead8:     	ldr	x12, [x15, #0x78]
10002eadc:     	umaddl	x0, w0, w13, x12
10002eae0:     	cbz	x0, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eae4:     	and	w12, w9, #0xffff
10002eae8:     	cmp	w12, #0xb
10002eaec:     	b.lo	0x10002eb0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6f4>
10002eaf0:     	and	w12, w9, #0xfffc
10002eaf4:     	cmp	w12, #0x54
10002eaf8:     	b.eq	0x10002eb0c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x6f4>
10002eafc:     	and	w13, w9, #0xffff
10002eb00:     	sub	w14, w13, #0x5c
10002eb04:     	cmp	w14, #0x2
10002eb08:     	b.hs	0x10002ebf4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7dc>
10002eb0c:     	ldr	x10, [sp, #0x20]
10002eb10:     	ldr	w8, [x0, #0x18]
10002eb14:     	ldr	x1, [x10, #0x78]
10002eb18:     	cmp	x1, x8
10002eb1c:     	b.ls	0x10002fb1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002eb20:     	ldr	x10, [x10, #0x70]
10002eb24:     	adds	x8, x10, x8, lsl #3
10002eb28:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eb2c:     	ldr	x8, [x8]
10002eb30:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eb34:     	ldr	x10, [x8, #0x10]
10002eb38:     	ldr	w8, [x0, #0x14]
10002eb3c:     	ldr	w11, [x0, #0x1c]
10002eb40:     	ldrb	w4, [x0, #0x40]
10002eb44:     	ldr	x12, [x10, #0x40]
10002eb48:     	orr	x3, x8, x11, lsl #32
10002eb4c:     	cbz	x12, 0x10002eb58 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x740>
10002eb50:     	ldr	x8, [x10, #0x60]
10002eb54:     	cbz	x8, 0x10002ed10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8f8>
10002eb58:     	sub	x8, x29, #0x78
10002eb5c:     	mov	x0, x10
10002eb60:     	mov	w1, w9
10002eb64:     	sub	x26, x29, #0x78
10002eb68:     	bl	0x10004c7a4 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).load>
10002eb6c:     	b	0x10002ed50 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x938>
10002eb70:     	mov	w8, #-0x1000001         ; =-16777217
10002eb74:     	add	w8, w9, w8
10002eb78:     	cmp	w8, #0x8
10002eb7c:     	b.hi	0x10002f600 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11e8>
10002eb80:     	mov	w11, #0x1               ; =1
10002eb84:     	mov	w12, #0x117             ; =279
10002eb88:     	lsl	w11, w11, w8
10002eb8c:     	tst	w11, w12
10002eb90:     	b.eq	0x10002f5f0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11d8>
10002eb94:     	and	w9, w9, #0xff
10002eb98:     	mov	w8, wzr
10002eb9c:     	cmp	w9, #0x4
10002eba0:     	b.gt	0x10002ee6c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa54>
10002eba4:     	cmp	w9, #0x2
10002eba8:     	b.eq	0x10002eebc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xaa4>
10002ebac:     	cmp	w9, #0x3
10002ebb0:     	b.ne	0x10002f544 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ebb4:     	sub	w8, w10, #0x1
10002ebb8:     	cmp	w8, w5
10002ebbc:     	csel	w8, w8, w5, lo
10002ebc0:     	b	0x10002f544 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ebc4:     	umulh	x12, x13, x14
10002ebc8:     	mov	w8, #0x13               ; =19
10002ebcc:     	movk	w8, #0xfc, lsl #16
10002ebd0:     	mul	x11, x13, x14
10002ebd4:     	add	w8, w8, #0x2
10002ebd8:     	cmp	w9, w8
10002ebdc:     	b.ne	0x10002ed9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x984>
10002ebe0:     	and	x8, x13, x14, asr #63
10002ebe4:     	and	x9, x14, x13, asr #63
10002ebe8:     	add	x8, x9, x8
10002ebec:     	sub	x12, x12, x8
10002ebf0:     	b	0x10002ed9c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x984>
10002ebf4:     	cmp	w13, #0xb
10002ebf8:     	ldr	x13, [sp, #0x20]
10002ebfc:     	b.eq	0x10002ec08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x7f0>
10002ec00:     	cmp	w12, #0x58
10002ec04:     	b.ne	0x10002ee98 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xa80>
10002ec08:     	ldr	x1, [x13, #0x78]
10002ec0c:     	ldr	w8, [x0, #0x18]
10002ec10:     	cmp	x1, x8
10002ec14:     	b.ls	0x10002fb1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002ec18:     	ldr	x10, [x13, #0x70]
10002ec1c:     	adds	x8, x10, x8, lsl #3
10002ec20:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ec24:     	ldr	x8, [x8]
10002ec28:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ec2c:     	ldr	x8, [x8, #0x10]
10002ec30:     	ldr	w10, [x0, #0x14]
10002ec34:     	ldr	w11, [x0, #0x1c]
10002ec38:     	ldrb	w4, [x0, #0x40]
10002ec3c:     	ldr	x12, [x8, #0x40]
10002ec40:     	orr	x3, x10, x11, lsl #32
10002ec44:     	cbz	x12, 0x10002ec50 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x838>
10002ec48:     	ldr	x10, [x8, #0x60]
10002ec4c:     	cbz	x10, 0x10002f514 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x10fc>
10002ec50:     	mov	x0, x8
10002ec54:     	mov	w1, w9
10002ec58:     	bl	0x10004a0f8 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).store>
10002ec5c:     	mov	w8, w0
10002ec60:     	mov	x0, xzr
10002ec64:     	mov	x1, xzr
10002ec68:     	and	w25, w8, #0xff
10002ec6c:     	b	0x10002ed5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002ec70:     	mov	x3, xzr
10002ec74:     	mov	x4, xzr
10002ec78:     	ldr	w0, [x27, #0x10]
10002ec7c:     	cmn	w0, #0x1
10002ec80:     	b.ne	0x10002e818 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x400>
10002ec84:     	mov	x5, xzr
10002ec88:     	mov	x6, xzr
10002ec8c:     	and	w8, w8, #0xffff
10002ec90:     	cmp	w8, #0xc
10002ec94:     	b.eq	0x10002e834 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x41c>
10002ec98:     	mov	x7, xzr
10002ec9c:     	mov	x9, xzr
10002eca0:     	cbz	x20, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eca4:     	ldr	x1, [x15, #0x80]
10002eca8:     	ldr	w0, [x27, #0x4]
10002ecac:     	cmp	x1, x0
10002ecb0:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ecb4:     	mov	w10, #0xe0              ; =224
10002ecb8:     	ldr	x8, [x15, #0x78]
10002ecbc:     	umaddl	x2, w0, w10, x8
10002ecc0:     	cbz	x2, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ecc4:     	ldr	x0, [x20]
10002ecc8:     	ldr	x1, [sp, #0x20]
10002eccc:     	add	x8, sp, #0x30
10002ecd0:     	str	x9, [sp]
10002ecd4:     	bl	0x10003a12c <_github.com/cpunion/wasmvm/internal/engine/state.(*Store).GCAccess>
10002ecd8:     	ldr	x25, [sp, #0x40]
10002ecdc:     	ldr	q0, [sp, #0x30]
10002ece0:     	cbz	x25, 0x10002ecec <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8d4>
10002ece4:     	ldr	x8, [x25, #0x8]
10002ece8:     	cbnz	x8, 0x10002f9e0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15c8>
10002ecec:     	ldr	w0, [x27, #0x8]
10002ecf0:     	ldr	x15, [sp, #0x28]
10002ecf4:     	cmn	w0, #0x1
10002ecf8:     	b.eq	0x10002ed08 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x8f0>
10002ecfc:     	cmp	x21, x0
10002ed00:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002ed04:     	str	q0, [x28, x0, lsl #4]
10002ed08:     	add	w27, w24, #0x1
10002ed0c:     	b	0x10002ede0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x9c8>
10002ed10:     	sub	x8, x29, #0x78
10002ed14:     	mov	x0, x10
10002ed18:     	mov	w1, w9
10002ed1c:     	b	0x10002ed48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x930>
10002ed20:     	ldr	w8, [x27, #0x18]
10002ed24:     	mov	x1, xzr
10002ed28:     	orr	x0, x8, x10, lsl #32
10002ed2c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ed30:     	sub	x8, x29, #0x78
10002ed34:     	mov	w1, w9
10002ed38:     	mov	x2, x5
10002ed3c:     	mov	w4, wzr
10002ed40:     	mov	x5, xzr
10002ed44:     	mov	x6, xzr
10002ed48:     	sub	x26, x29, #0x78
10002ed4c:     	bl	0x10004c944 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).loadLocked>
10002ed50:     	ldp	x0, x1, [x26]
10002ed54:     	ldrb	w25, [x26, #0x10]
10002ed58:     	ldr	w26, [sp, #0x14]
10002ed5c:     	ldr	x15, [sp, #0x28]
10002ed60:     	tst	w25, #0xff
10002ed64:     	b.ne	0x10002fa48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002ed68:     	ldr	w8, [x27, #0x8]
10002ed6c:     	cmn	w8, #0x1
10002ed70:     	b.eq	0x10002e4d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc0>
10002ed74:     	cmp	x21, x8
10002ed78:     	b.hi	0x10002e4d0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xb8>
10002ed7c:     	b	0x10002fb10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f8>
10002ed80:     	sub	x11, x14, x12
10002ed84:     	eon	x9, x14, x12
10002ed88:     	bic	x12, x12, x14
10002ed8c:     	and	x9, x11, x9
10002ed90:     	orr	x9, x9, x12
10002ed94:     	add	x9, x13, x9, asr #63
10002ed98:     	sub	x12, x9, x8
10002ed9c:     	ldr	w0, [x27, #0x1c]
10002eda0:     	cmp	x1, x0
10002eda4:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002eda8:     	add	w8, w0, #0x1
10002edac:     	cmp	x1, x8
10002edb0:     	b.ls	0x10002fb1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002edb4:     	ldr	w9, [x10, x0, lsl #2]
10002edb8:     	cmp	x21, x9
10002edbc:     	b.ls	0x10002fb70 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1758>
10002edc0:     	ldr	w0, [x10, x8, lsl #2]
10002edc4:     	add	x8, x28, x9, lsl #4
10002edc8:     	cmp	x21, x0
10002edcc:     	stp	x11, xzr, [x8]
10002edd0:     	b.ls	0x10002fb24 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x170c>
10002edd4:     	add	x8, x28, x0, lsl #4
10002edd8:     	add	w27, w24, #0x1
10002eddc:     	stp	x12, xzr, [x8]
10002ede0:     	ldr	x8, [sp, #0x18]
10002ede4:     	stur	w27, [x8, #-0x38]
10002ede8:     	b	0x10002e4dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc4>
10002edec:     	ldr	w0, [x27, #0x18]
10002edf0:     	cmp	x21, x0
10002edf4:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002edf8:     	add	x8, x28, x0, lsl #4
10002edfc:     	mov	x0, x5
10002ee00:     	mov	x1, x4
10002ee04:     	stp	x5, x4, [x8]
10002ee08:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ee0c:     	ldr	w0, [x27, #0x14]
10002ee10:     	cmn	w0, #0x1
10002ee14:     	b.eq	0x10002f530 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1118>
10002ee18:     	cmp	x21, x0
10002ee1c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002ee20:     	lsl	x8, x0, #4
10002ee24:     	ldr	x8, [x28, x8]
10002ee28:     	tst	x8, #0xffffffff
10002ee2c:     	csel	x0, x7, x5, eq
10002ee30:     	csel	x1, x6, x4, eq
10002ee34:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ee38:     	mov	w1, w9
10002ee3c:     	mov	x2, x5
10002ee40:     	mov	w4, wzr
10002ee44:     	mov	x5, x7
10002ee48:     	b	0x10002f51c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1104>
10002ee4c:     	cmp	x5, #0x0
10002ee50:     	mov	x1, xzr
10002ee54:     	cset	w0, eq
10002ee58:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ee5c:     	cmp	x5, x7
10002ee60:     	mov	x1, xzr
10002ee64:     	cset	w0, eq
10002ee68:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ee6c:     	cmp	w9, #0x9
10002ee70:     	b.ne	0x10002f53c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1124>
10002ee74:     	cmp	x5, #0x0
10002ee78:     	cset	w8, ne
10002ee7c:     	b	0x10002f544 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002ee80:     	sxtw	x0, w5
10002ee84:     	mov	x1, xzr
10002ee88:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ee8c:     	mov	x1, xzr
10002ee90:     	mov	w0, w5
10002ee94:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ee98:     	mov	x1, x2
10002ee9c:     	mov	x2, x8
10002eea0:     	mov	x3, x5
10002eea4:     	mov	x4, x6
10002eea8:     	mov	x5, x10
10002eeac:     	mov	x6, x11
10002eeb0:     	bl	0x100052b10 <_github.com/cpunion/wasmvm/internal/engine/simd.Eval>
10002eeb4:     	ldr	x15, [sp, #0x28]
10002eeb8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002eebc:     	cmp	w5, #0x0
10002eec0:     	cset	w8, eq
10002eec4:     	b	0x10002f544 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x112c>
10002eec8:     	ldr	x8, [sp, #0x20]
10002eecc:     	ldr	w0, [x27, #0x18]
10002eed0:     	ldr	x1, [x8, #0x48]
10002eed4:     	cmp	x1, x0
10002eed8:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002eedc:     	ldr	x8, [x8, #0x40]
10002eee0:     	mov	x1, xzr
10002eee4:     	ldr	x0, [x8, x0, lsl #3]
10002eee8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002eeec:     	ldr	x8, [sp, #0x20]
10002eef0:     	ldr	w0, [x27, #0x18]
10002eef4:     	ldr	x1, [x8, #0x60]
10002eef8:     	cmp	x1, x0
10002eefc:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ef00:     	ldr	x8, [x8, #0x58]
10002ef04:     	adds	x8, x8, x0, lsl #3
10002ef08:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ef0c:     	ldr	x8, [x8]
10002ef10:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ef14:     	ldrb	w9, [x8, #0x29]
10002ef18:     	mov	w10, w5
10002ef1c:     	ldr	x11, [x8, #0x40]
10002ef20:     	cmp	w9, #0x0
10002ef24:     	csel	x9, x5, x10, ne
10002ef28:     	cmp	x9, x11
10002ef2c:     	b.hs	0x10002fa34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x161c>
10002ef30:     	ldr	x8, [x8, #0x38]
10002ef34:     	mov	x0, xzr
10002ef38:     	mov	x1, xzr
10002ef3c:     	str	x7, [x8, x9, lsl #3]
10002ef40:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef44:     	ldr	x8, [sp, #0x20]
10002ef48:     	ldr	w0, [x27, #0x18]
10002ef4c:     	ldr	x1, [x8, #0x60]
10002ef50:     	cmp	x1, x0
10002ef54:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002ef58:     	ldr	x8, [x8, #0x58]
10002ef5c:     	adds	x8, x8, x0, lsl #3
10002ef60:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ef64:     	ldr	x8, [x8]
10002ef68:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002ef6c:     	ldrb	w9, [x8, #0x29]
10002ef70:     	mov	w10, w5
10002ef74:     	ldr	x11, [x8, #0x40]
10002ef78:     	cmp	w9, #0x0
10002ef7c:     	csel	x9, x5, x10, ne
10002ef80:     	cmp	x9, x11
10002ef84:     	b.hs	0x10002fa34 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x161c>
10002ef88:     	ldr	x8, [x8, #0x38]
10002ef8c:     	mov	x1, xzr
10002ef90:     	ldr	x0, [x8, x9, lsl #3]
10002ef94:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002ef98:     	ldr	x8, [sp, #0x20]
10002ef9c:     	ldr	w0, [x27, #0x18]
10002efa0:     	ldr	x1, [x8, #0x90]
10002efa4:     	cmp	x1, x0
10002efa8:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002efac:     	ldr	x8, [x8, #0x88]
10002efb0:     	ldr	x8, [x8, x0, lsl #3]
10002efb4:     	ldp	x0, x1, [x8, #0x20]
10002efb8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002efbc:     	ldr	w0, [x27, #0x18]
10002efc0:     	mov	x1, xzr
10002efc4:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002efc8:     	ldr	x8, [sp, #0x20]
10002efcc:     	ldr	w0, [x27, #0x18]
10002efd0:     	ldr	x1, [x8, #0x78]
10002efd4:     	cmp	x1, x0
10002efd8:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002efdc:     	ldr	x8, [x8, #0x70]
10002efe0:     	adds	x8, x8, x0, lsl #3
10002efe4:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002efe8:     	cbz	x20, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002efec:     	ldr	x25, [x8]
10002eff0:     	cbz	x25, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002eff4:     	ldr	x8, [x25, #0x10]
10002eff8:     	ldr	x0, [x20]
10002effc:     	mov	w9, w5
10002f000:     	mov	x1, x25
10002f004:     	ldrb	w8, [x8, #0x31]
10002f008:     	tst	w8, #0x1
10002f00c:     	strb	w8, [sp, #0x5e]
10002f010:     	csel	x2, x5, x9, ne
10002f014:     	bl	0x100046d08 <_github.com/cpunion/wasmvm/internal/engine/state.(*Store).GrowMemory>
10002f018:     	tbz	w1, #0x0, 0x10002f758 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1340>
10002f01c:     	ldr	x15, [sp, #0x28]
10002f020:     	mov	x1, xzr
10002f024:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f028:     	mov	x0, x5
10002f02c:     	mov	x1, x4
10002f030:     	cbnz	x5, 0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f034:     	b	0x10002fa44 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x162c>
10002f038:     	ldr	x9, [sp, #0x20]
10002f03c:     	ldr	w8, [x27, #0x18]
10002f040:     	ldr	x1, [x9, #0x90]
10002f044:     	cmp	x1, x8
10002f048:     	b.ls	0x10002fb1c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1704>
10002f04c:     	ldr	x9, [x9, #0x88]
10002f050:     	mov	x0, xzr
10002f054:     	mov	x1, xzr
10002f058:     	ldr	x8, [x9, x8, lsl #3]
10002f05c:     	stp	x5, x4, [x8, #0x20]
10002f060:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f064:     	ldr	w0, [x27, #0x18]
10002f068:     	cmp	x21, x0
10002f06c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002f070:     	add	x8, x28, x0, lsl #4
10002f074:     	ldp	x0, x1, [x8]
10002f078:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f07c:     	ldr	x8, [sp, #0x20]
10002f080:     	ldr	w0, [x27, #0x18]
10002f084:     	ldr	x1, [x8, #0x78]
10002f088:     	cmp	x1, x0
10002f08c:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f090:     	ldr	x8, [x8, #0x70]
10002f094:     	adds	x8, x8, x0, lsl #3
10002f098:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f09c:     	ldr	x8, [x8]
10002f0a0:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f0a4:     	ldr	x0, [x8, #0x10]
10002f0a8:     	ldr	x9, [x0, #0x40]
10002f0ac:     	ldr	x8, [x0, #0x60]
10002f0b0:     	cbz	x9, 0x10002f79c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1384>
10002f0b4:     	cbnz	x8, 0x10002f7a0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1388>
10002f0b8:     	bl	0x100049998 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).pagesLocked>
10002f0bc:     	ldr	x15, [sp, #0x28]
10002f0c0:     	mov	x1, xzr
10002f0c4:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0c8:     	mov	x1, xzr
10002f0cc:     	eor	x0, x7, x5
10002f0d0:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0d4:     	lsr	x0, x5, x7
10002f0d8:     	mov	x1, xzr
10002f0dc:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0e0:     	lsr	w0, w5, w7
10002f0e4:     	mov	x1, xzr
10002f0e8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0ec:     	mov	x1, xzr
10002f0f0:     	orr	x0, x7, x5
10002f0f4:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f0f8:     	asr	w0, w5, w7
10002f0fc:     	mov	x1, xzr
10002f100:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f104:     	mov	x1, xzr
10002f108:     	eor	w0, w7, w5
10002f10c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f110:     	lsr	x8, x5, #1
10002f114:     	and	x9, x5, #0x5555555555555555
10002f118:     	mov	x1, xzr
10002f11c:     	and	x8, x8, #0x5555555555555555
10002f120:     	add	x8, x8, x9
10002f124:     	lsr	x9, x8, #2
10002f128:     	and	x8, x8, #0x3333333333333333
10002f12c:     	and	x9, x9, #0x3333333333333333
10002f130:     	add	x8, x9, x8
10002f134:     	add	x8, x8, x8, lsr #4
10002f138:     	and	x8, x8, #0xf0f0f0f0f0f0f0f
10002f13c:     	add	x8, x8, x8, lsr #8
10002f140:     	add	x8, x8, x8, lsr #16
10002f144:     	add	x8, x8, x8, lsr #32
10002f148:     	and	x0, x8, #0x7f
10002f14c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f150:     	cmp	x5, x7
10002f154:     	mov	x1, xzr
10002f158:     	cset	w0, lo
10002f15c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f160:     	cmp	x5, x7
10002f164:     	mov	x1, xzr
10002f168:     	cset	w0, ls
10002f16c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f170:     	cmp	x5, x7
10002f174:     	mov	x1, xzr
10002f178:     	cset	w0, hi
10002f17c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f180:     	mul	w0, w7, w5
10002f184:     	mov	x1, xzr
10002f188:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f18c:     	lsr	w8, w5, #16
10002f190:     	mov	x10, #-0x10             ; =-16
10002f194:     	mov	w11, #0x20              ; =32
10002f198:     	mov	w12, #0x18              ; =24
10002f19c:     	mov	x1, xzr
10002f1a0:     	cmp	w8, #0x0
10002f1a4:     	csel	w8, w8, w5, ne
10002f1a8:     	csel	x10, x10, xzr, ne
10002f1ac:     	lsr	w9, w8, #8
10002f1b0:     	cmp	w8, #0xff
10002f1b4:     	csel	w8, w9, w8, hi
10002f1b8:     	adrp	x9, 0x1002e4000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x10c0>
10002f1bc:     	add	x9, x9, #0x5f5
10002f1c0:     	and	x8, x8, #0xff
10002f1c4:     	ldrb	w8, [x9, x8]
10002f1c8:     	csel	x9, x12, x11, hi
10002f1cc:     	add	x9, x9, x10
10002f1d0:     	sub	x0, x9, x8
10002f1d4:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f1d8:     	neg	w8, w7
10002f1dc:     	mov	x1, xzr
10002f1e0:     	ror	w0, w5, w8
10002f1e4:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f1e8:     	cmp	x5, x7
10002f1ec:     	mov	x1, xzr
10002f1f0:     	cset	w0, gt
10002f1f4:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f1f8:     	cmp	w5, w7
10002f1fc:     	mov	x1, xzr
10002f200:     	cset	w0, lt
10002f204:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f208:     	cmp	x5, x7
10002f20c:     	mov	x1, xzr
10002f210:     	cset	w0, ne
10002f214:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f218:     	cbz	w5, 0x10002f844 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x142c>
10002f21c:     	neg	w8, w5
10002f220:     	mov	w9, #0xb531             ; =46385
10002f224:     	mov	x1, xzr
10002f228:     	and	w8, w5, w8
10002f22c:     	movk	w9, #0x77c, lsl #16
10002f230:     	mul	w8, w8, w9
10002f234:     	adrp	x9, 0x1002e4000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x10c0>
10002f238:     	add	x9, x9, #0x5d5
10002f23c:     	lsr	w8, w8, #27
10002f240:     	ldrb	w0, [x9, w8, uxtw]
10002f244:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f248:     	mov	x1, xzr
10002f24c:     	add	w0, w7, w5
10002f250:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f254:     	cmp	w5, w7
10002f258:     	mov	x1, xzr
10002f25c:     	cset	w0, ne
10002f260:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f264:     	cmp	x5, x7
10002f268:     	mov	x1, xzr
10002f26c:     	cset	w0, le
10002f270:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f274:     	ror	w0, w5, w7
10002f278:     	mov	x1, xzr
10002f27c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f280:     	cmp	w5, w7
10002f284:     	mov	x1, xzr
10002f288:     	cset	w0, ls
10002f28c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f290:     	cmp	w5, w7
10002f294:     	mov	x1, xzr
10002f298:     	cset	w0, ge
10002f29c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2a0:     	cmp	w5, #0x0
10002f2a4:     	mov	x1, xzr
10002f2a8:     	cset	w0, eq
10002f2ac:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2b0:     	cmp	w5, w7
10002f2b4:     	mov	x1, xzr
10002f2b8:     	cset	w0, lo
10002f2bc:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f2c0:     	lsr	w8, w5, #24
10002f2c4:     	ubfx	x10, x5, #16, #8
10002f2c8:     	ubfx	x11, x5, #8, #8
10002f2cc:     	and	x9, x5, #0xff
10002f2d0:     	adrp	x12, 0x1002c9000 <dyld_stub_binder+0x1002c9000>
10002f2d4:     	add	x12, x12, #0xbaf
10002f2d8:     	ldrb	w8, [x12, x8]
10002f2dc:     	ldrb	w10, [x12, x10]
10002f2e0:     	ldrb	w11, [x12, x11]
10002f2e4:     	ldrb	w9, [x12, x9]
10002f2e8:     	mov	x1, xzr
10002f2ec:     	add	w8, w10, w8
10002f2f0:     	add	w9, w11, w9
10002f2f4:     	add	w8, w8, w9
10002f2f8:     	and	x0, x8, #0xff
10002f2fc:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f300:     	mov	x1, xzr
10002f304:     	add	x0, x7, x5
10002f308:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f30c:     	lsl	w0, w5, w7
10002f310:     	mov	x1, xzr
10002f314:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f318:     	mov	x1, xzr
10002f31c:     	and	x0, x7, x5
10002f320:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f324:     	mov	x1, xzr
10002f328:     	sub	x0, x5, x7
10002f32c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f330:     	cmp	w5, w7
10002f334:     	mov	x1, xzr
10002f338:     	cset	w0, eq
10002f33c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f340:     	neg	w8, w7
10002f344:     	mov	x1, xzr
10002f348:     	ror	x0, x5, x8
10002f34c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f350:     	cmp	w5, w7
10002f354:     	mov	x1, xzr
10002f358:     	cset	w0, hs
10002f35c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f360:     	mov	x1, xzr
10002f364:     	and	w0, w5, w7
10002f368:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f36c:     	cmp	w5, w7
10002f370:     	mov	x1, xzr
10002f374:     	cset	w0, le
10002f378:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f37c:     	cmp	w5, w7
10002f380:     	mov	x1, xzr
10002f384:     	cset	w0, gt
10002f388:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f38c:     	lsr	x8, x5, #32
10002f390:     	mov	w10, #0x20              ; =32
10002f394:     	mov	x1, xzr
10002f398:     	cmp	x8, #0x0
10002f39c:     	csel	x8, x8, x5, ne
10002f3a0:     	csel	x10, x10, xzr, ne
10002f3a4:     	lsr	x9, x8, #16
10002f3a8:     	cmp	x9, #0x0
10002f3ac:     	csel	x8, x9, x8, ne
10002f3b0:     	orr	x9, x10, #0x10
10002f3b4:     	lsr	x11, x8, #8
10002f3b8:     	csel	x9, x9, x10, ne
10002f3bc:     	cmp	x8, #0xff
10002f3c0:     	orr	x10, x9, #0x8
10002f3c4:     	csel	x8, x11, x8, hi
10002f3c8:     	adrp	x11, 0x1002e4000 <__llgo_ifacebox$O4qBo0EAyIIrlNtrmtT79e8Z3f5Yx6QzEL2rBxTG5wY+0x10c0>
10002f3cc:     	add	x11, x11, #0x5f5
10002f3d0:     	and	x8, x8, #0xff
10002f3d4:     	csel	x9, x10, x9, hi
10002f3d8:     	mov	w10, #0x40              ; =64
10002f3dc:     	ldrb	w8, [x11, x8]
10002f3e0:     	sub	x9, x10, x9
10002f3e4:     	sub	x0, x9, x8
10002f3e8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3ec:     	cmp	x5, x7
10002f3f0:     	mov	x1, xzr
10002f3f4:     	cset	w0, lt
10002f3f8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f3fc:     	ror	x0, x5, x7
10002f400:     	mov	x1, xzr
10002f404:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f408:     	cmp	w5, w7
10002f40c:     	mov	x1, xzr
10002f410:     	cset	w0, hi
10002f414:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f418:     	sxtb	x0, w5
10002f41c:     	mov	x1, xzr
10002f420:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f424:     	sxth	x0, w5
10002f428:     	mov	x1, xzr
10002f42c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f430:     	cmp	x5, x7
10002f434:     	mov	x1, xzr
10002f438:     	cset	w0, hs
10002f43c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f440:     	cmp	x5, x7
10002f444:     	mov	x1, xzr
10002f448:     	cset	w0, ge
10002f44c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f450:     	asr	x0, x5, x7
10002f454:     	mov	x1, xzr
10002f458:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f45c:     	mov	x1, xzr
10002f460:     	orr	w0, w7, w5
10002f464:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f468:     	mov	x1, xzr
10002f46c:     	sub	w0, w5, w7
10002f470:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f474:     	mul	x0, x7, x5
10002f478:     	mov	x1, xzr
10002f47c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f480:     	sxtb	x8, w5
10002f484:     	mov	x1, xzr
10002f488:     	mov	w0, w8
10002f48c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f490:     	rbit	x8, x5
10002f494:     	mov	x1, xzr
10002f498:     	clz	x0, x8
10002f49c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4a0:     	lsl	x0, x5, x7
10002f4a4:     	mov	x1, xzr
10002f4a8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4ac:     	sxth	x8, w5
10002f4b0:     	mov	x1, xzr
10002f4b4:     	mov	w0, w8
10002f4b8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f4bc:     	sub	w8, w9, #0x8b
10002f4c0:     	cmp	w8, #0x34
10002f4c4:     	b.hi	0x10002f4e4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x10cc>
10002f4c8:     	mov	w10, #0x1               ; =1
10002f4cc:     	lsl	x8, x10, x8
10002f4d0:     	mov	x10, #-0x10000001       ; =-268435457
10002f4d4:     	movk	x10, #0xfff9, lsl #32
10002f4d8:     	movk	x10, #0x1f, lsl #48
10002f4dc:     	tst	x8, x10
10002f4e0:     	b.ne	0x10002e8cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b4>
10002f4e4:     	sub	w8, w9, #0x61
10002f4e8:     	mov	x0, xzr
10002f4ec:     	cmp	w8, #0x21
10002f4f0:     	b.hi	0x10002f8fc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14e4>
10002f4f4:     	mov	w10, #0x1               ; =1
10002f4f8:     	mov	x1, x0
10002f4fc:     	lsl	x8, x10, x8
10002f500:     	mov	x10, #0x3c0000000       ; =16106127360
10002f504:     	movk	x10, #0xf03f
10002f508:     	tst	x8, x10
10002f50c:     	b.ne	0x10002e8cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x4b4>
10002f510:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f514:     	mov	x0, x8
10002f518:     	mov	w1, w9
10002f51c:     	bl	0x10004a378 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).storeLocked>
10002f520:     	mov	w25, w0
10002f524:     	mov	x0, xzr
10002f528:     	mov	x1, xzr
10002f52c:     	b	0x10002ed5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002f530:     	mov	x0, x7
10002f534:     	mov	x1, x6
10002f538:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f53c:     	cmp	w9, #0x5
10002f540:     	b.eq	0x10002fa3c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1624>
10002f544:     	ldr	w9, [x27, #0x18]
10002f548:     	ldr	x1, [x15, #0xe8]
10002f54c:     	add	w0, w9, w8
10002f550:     	cmp	x1, x0
10002f554:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f558:     	ldr	x8, [x15, #0xe0]
10002f55c:     	mov	w9, #0xc                ; =12
10002f560:     	umaddl	x8, w0, w9, x8
10002f564:     	ldp	w27, w25, [x8]
10002f568:     	ldr	w10, [x8, #0x8]
10002f56c:     	cmp	w27, w24
10002f570:     	b.hi	0x10002f7d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f574:     	ldr	x8, [x20, #0x160]
10002f578:     	cbz	x8, 0x10002f588 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1170>
10002f57c:     	cbz	x20, 0x10002f5d4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11bc>
10002f580:     	ldar	w8, [x8]
10002f584:     	cbz	w8, 0x10002f7c4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13ac>
10002f588:     	ldr	x8, [x20, #0x8]
10002f58c:     	cbz	x8, 0x10002fbb4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x179c>
10002f590:     	ldr	x9, [x8, #0x8]
10002f594:     	ldr	x0, [x20, #0x10]
10002f598:     	ldrb	w9, [x9, #0x17]
10002f59c:     	tbz	w9, #0x5, 0x10002f77c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1364>
10002f5a0:     	and	w9, w9, #0x1f
10002f5a4:     	mov	w24, w10
10002f5a8:     	cmp	w9, #0x16
10002f5ac:     	b.eq	0x10002f5b8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x11a0>
10002f5b0:     	stur	x0, [x29, #-0x78]
10002f5b4:     	sub	x0, x29, #0x78
10002f5b8:     	ldr	x8, [x8, #0x28]
10002f5bc:     	blr	x8
10002f5c0:     	ldr	x15, [sp, #0x28]
10002f5c4:     	mov	x2, x1
10002f5c8:     	mov	w10, w24
10002f5cc:     	cbnz	x0, 0x10002f7d0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13b8>
10002f5d0:     	b	0x10002f7d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f5d4:     	mov	w0, #0x1                ; =1
10002f5d8:     	mov	w24, w10
10002f5dc:     	bl	0x10021a0a4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002f5e0:     	ldr	x15, [sp, #0x28]
10002f5e4:     	mov	w10, w24
10002f5e8:     	cbnz	x0, 0x10002f7d0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13b8>
10002f5ec:     	b	0x10002f7d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f5f0:     	cmp	w8, #0x3
10002f5f4:     	b.eq	0x10002f954 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x153c>
10002f5f8:     	cmp	w8, #0x6
10002f5fc:     	b.eq	0x10002f904 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14ec>
10002f600:     	sub	w8, w9, #0x12
10002f604:     	cmp	w8, #0x4
10002f608:     	b.lo	0x10002f92c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1514>
10002f60c:     	ldr	w0, [x27, #0x14]
10002f610:     	cmn	w0, #0x1
10002f614:     	b.eq	0x10002f664 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x124c>
10002f618:     	cmp	x21, x0
10002f61c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002f620:     	add	x8, x28, x0, lsl #4
10002f624:     	ldp	x10, x11, [x8]
10002f628:     	and	w8, w9, #0xffff0000
10002f62c:     	cmp	w8, #0xfe0, lsl #12     ; =0xfe0000
10002f630:     	b.ne	0x10002f678 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1260>
10002f634:     	and	w4, w9, #0xffff
10002f638:     	cmp	w4, #0x3
10002f63c:     	b.ne	0x10002f850 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1438>
10002f640:     	cbz	x20, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f644:     	ldr	x8, [x20]
10002f648:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f64c:     	ldr	x0, [x8, #0x8]
10002f650:     	bl	0x100048fa0 <_github.com/cpunion/wasmvm/internal/engine/memory.(*Domain).Fence>
10002f654:     	ldr	x15, [sp, #0x28]
10002f658:     	mov	x0, xzr
10002f65c:     	mov	x1, xzr
10002f660:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f664:     	mov	x10, xzr
10002f668:     	mov	x11, xzr
10002f66c:     	and	w8, w9, #0xffff0000
10002f670:     	cmp	w8, #0xfe0, lsl #12     ; =0xfe0000
10002f674:     	b.eq	0x10002f634 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x121c>
10002f678:     	mov	w8, #0xffed             ; =65517
10002f67c:     	movk	w8, #0xff03, lsl #16
10002f680:     	add	w8, w8, w9
10002f684:     	add	w8, w8, #0xb
10002f688:     	cmp	w8, #0xa
10002f68c:     	b.hs	0x10002fbe4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17cc>
10002f690:     	ldr	x1, [x15, #0x80]
10002f694:     	ldr	w0, [x27, #0x4]
10002f698:     	cmp	x1, x0
10002f69c:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f6a0:     	ldr	x8, [x15, #0x78]
10002f6a4:     	mov	w9, #0xe0               ; =224
10002f6a8:     	umaddl	x8, w0, w9, x8
10002f6ac:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f6b0:     	ldr	w9, [x8]
10002f6b4:     	ldr	x12, [x8, #0x8]
10002f6b8:     	add	x2, sp, #0x88
10002f6bc:     	ldr	q0, [x8, #0x10]
10002f6c0:     	ldur	q1, [x8, #0x48]
10002f6c4:     	mov	x0, x20
10002f6c8:     	str	w9, [sp, #0x88]
10002f6cc:     	ldur	q2, [x8, #0x68]
10002f6d0:     	mov	x3, x5
10002f6d4:     	str	x12, [sp, #0x90]
10002f6d8:     	ldp	x9, x12, [x8, #0x20]
10002f6dc:     	stur	q0, [sp, #0x98]
10002f6e0:     	mov	x5, x7
10002f6e4:     	mov	x7, x10
10002f6e8:     	ldr	q0, [x8, #0x30]
10002f6ec:     	ldr	x1, [sp, #0x20]
10002f6f0:     	stp	x9, x12, [sp, #0xa8]
10002f6f4:     	add	x12, sp, #0x88
10002f6f8:     	ldrb	w9, [x8, #0x40]
10002f6fc:     	str	q0, [x12, #0x30]
10002f700:     	ldur	q0, [x8, #0x58]
10002f704:     	strb	w9, [sp, #0xc8]
10002f708:     	stur	q1, [x12, #0x48]
10002f70c:     	ldur	q1, [x8, #0x78]
10002f710:     	stur	q0, [x12, #0x58]
10002f714:     	ldur	q0, [x8, #0x88]
10002f718:     	ldp	x9, x8, [x8, #0x98]
10002f71c:     	stur	q2, [x12, #0x68]
10002f720:     	stur	q1, [x12, #0x78]
10002f724:     	stur	q0, [x12, #0x88]
10002f728:     	stp	x9, x8, [sp, #0x120]
10002f72c:     	add	x8, sp, #0x60
10002f730:     	str	x11, [sp]
10002f734:     	bl	0x100033c68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).bulk>
10002f738:     	ldp	x0, x1, [sp, #0x60]
10002f73c:     	ldr	x8, [sp, #0x78]
10002f740:     	ldrb	w25, [sp, #0x70]
10002f744:     	cbz	x8, 0x10002ed5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002f748:     	ldr	x9, [x8, #0x8]
10002f74c:     	ldr	x15, [sp, #0x28]
10002f750:     	cbz	x9, 0x10002ed60 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x948>
10002f754:     	b	0x10002fad8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16c0>
10002f758:     	ldr	x8, [x25, #0x10]
10002f75c:     	ldr	x15, [sp, #0x28]
10002f760:     	mov	x1, xzr
10002f764:     	ldrb	w8, [x8, #0x31]
10002f768:     	tst	w8, #0x1
10002f76c:     	strb	w8, [sp, #0x5f]
10002f770:     	mov	w8, #-0x1               ; =-1
10002f774:     	csinv	x0, x8, xzr, eq
10002f778:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f77c:     	mov	w24, w10
10002f780:     	ldr	x8, [x8, #0x28]
10002f784:     	blr	x8
10002f788:     	ldr	x15, [sp, #0x28]
10002f78c:     	mov	x2, x1
10002f790:     	mov	w10, w24
10002f794:     	cbnz	x0, 0x10002f7d0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13b8>
10002f798:     	b	0x10002f7d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f79c:     	cbz	x8, 0x10002f8e0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14c8>
10002f7a0:     	ldar	x8, [x8]
10002f7a4:     	cbz	x0, 0x10002f8e8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14d0>
10002f7a8:     	ldr	x9, [x0, #0x38]
10002f7ac:     	mov	w10, #0x10000           ; =65536
10002f7b0:     	mov	x1, xzr
10002f7b4:     	cmp	x9, #0x0
10002f7b8:     	csel	x9, x10, x9, eq
10002f7bc:     	udiv	x0, x8, x9
10002f7c0:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f7c4:     	mov	x0, xzr
10002f7c8:     	mov	x2, xzr
10002f7cc:     	b	0x10002f7d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13c0>
10002f7d0:     	ldr	x8, [x0, #0x8]
10002f7d4:     	cbnz	x8, 0x10002f9cc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15b4>
10002f7d8:     	ldr	x8, [x15, #0x108]
10002f7dc:     	add	w2, w10, w25
10002f7e0:     	cmp	x8, x2
10002f7e4:     	b.lo	0x10002fb38 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1720>
10002f7e8:     	cmp	w25, w2
10002f7ec:     	b.hi	0x10002fba4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x178c>
10002f7f0:     	sub	x8, x8, x25
10002f7f4:     	lsl	x9, x25, #3
10002f7f8:     	cmp	x8, #0x0
10002f7fc:     	csel	x8, x9, xzr, gt
10002f800:     	cbz	w10, 0x10002e4dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc4>
10002f804:     	ldr	x9, [x15, #0xf8]
10002f808:     	add	x8, x9, x8
10002f80c:     	sub	x9, x2, x25
10002f810:     	add	x10, x8, #0x4
10002f814:     	ldr	w0, [x10]
10002f818:     	cmp	x21, x0
10002f81c:     	b.ls	0x10002fafc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16e4>
10002f820:     	ldur	w8, [x10, #-0x4]
10002f824:     	cmp	x21, x8
10002f828:     	b.ls	0x10002fb10 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16f8>
10002f82c:     	ldr	q0, [x28, x0, lsl #4]
10002f830:     	subs	x9, x9, #0x1
10002f834:     	add	x10, x10, #0x8
10002f838:     	str	q0, [x28, x8, lsl #4]
10002f83c:     	b.ne	0x10002f814 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x13fc>
10002f840:     	b	0x10002e4dc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0xc4>
10002f844:     	mov	x1, xzr
10002f848:     	mov	w0, #0x20               ; =32
10002f84c:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f850:     	ldr	x1, [x15, #0x80]
10002f854:     	ldr	w0, [x27, #0x4]
10002f858:     	cmp	x1, x0
10002f85c:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f860:     	ldr	x8, [x15, #0x78]
10002f864:     	mov	w9, #0xe0               ; =224
10002f868:     	umaddl	x8, w0, w9, x8
10002f86c:     	cbz	x8, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f870:     	ldr	x9, [sp, #0x20]
10002f874:     	ldr	w0, [x8, #0x18]
10002f878:     	ldr	x1, [x9, #0x78]
10002f87c:     	cmp	x1, x0
10002f880:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002f884:     	ldr	x9, [x9, #0x70]
10002f888:     	adds	x9, x9, x0, lsl #3
10002f88c:     	b.eq	0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f890:     	ldr	x9, [x9]
10002f894:     	cbz	x9, 0x10002f974 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x155c>
10002f898:     	ldp	x11, x1, [x20]
10002f89c:     	ldr	w12, [x8, #0x14]
10002f8a0:     	ldr	w8, [x8, #0x1c]
10002f8a4:     	ldr	x2, [x20, #0x10]
10002f8a8:     	ldr	x0, [x9, #0x10]
10002f8ac:     	ldr	x3, [x11, #0x8]
10002f8b0:     	orr	x6, x12, x8, lsl #32
10002f8b4:     	sub	x8, x29, #0xa0
10002f8b8:     	str	x10, [sp]
10002f8bc:     	bl	0x10004ae6c <_github.com/cpunion/wasmvm/internal/engine/memory.(*Memory).Atomic>
10002f8c0:     	ldp	x0, x1, [x29, #-0xa0]
10002f8c4:     	ldur	x8, [x29, #-0x88]
10002f8c8:     	ldurb	w25, [x29, #-0x90]
10002f8cc:     	cbz	x8, 0x10002ed5c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x944>
10002f8d0:     	ldr	x9, [x8, #0x8]
10002f8d4:     	ldr	x15, [sp, #0x28]
10002f8d8:     	cbz	x9, 0x10002ed60 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x948>
10002f8dc:     	b	0x10002fae0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16c8>
10002f8e0:     	ldr	x8, [x0, #0x8]
10002f8e4:     	cbnz	x0, 0x10002f7a8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1390>
10002f8e8:     	mov	w0, #0x1                ; =1
10002f8ec:     	bl	0x10021a0a4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002f8f0:     	ldr	x15, [sp, #0x28]
10002f8f4:     	mov	x1, xzr
10002f8f8:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f8fc:     	mov	x1, x0
10002f900:     	b	0x10002ed68 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x950>
10002f904:     	ldr	x8, [sp, #0x18]
10002f908:     	mov	x0, x20
10002f90c:     	stur	w24, [x8, #-0x38]
10002f910:     	bl	0x1000318d0 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runRuntime>
10002f914:     	tst	w0, #0xff
10002f918:     	b.ne	0x10002f9d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x15c0>
10002f91c:     	ldr	x8, [x20, #0x48]
10002f920:     	cmp	x8, #0x0
10002f924:     	b.gt	0x10002e454 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x3c>
10002f928:     	b	0x10002fbcc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17b4>
10002f92c:     	ldr	x8, [sp, #0x18]
10002f930:     	mov	x0, x20
10002f934:     	stur	w24, [x8, #-0x38]
10002f938:     	bl	0x1000309bc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runCall>
10002f93c:     	b	0x10002f914 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14fc>
10002f940:     	ldr	x8, [sp, #0x18]
10002f944:     	mov	x0, x20
10002f948:     	stur	w24, [x8, #-0x38]
10002f94c:     	bl	0x1000314d8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runThrow>
10002f950:     	b	0x10002f914 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x14fc>
10002f954:     	ldr	x8, [sp, #0x18]
10002f958:     	mov	x0, x20
10002f95c:     	stur	w24, [x8, #-0x38]
10002f960:     	bl	0x1000305f4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).returnFrame>
10002f964:     	and	w8, w0, #0xff
10002f968:     	tbz	w8, #0x0, 0x10002f91c <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1504>
10002f96c:     	mov	w0, #0x1                ; =1
10002f970:     	b	0x10002fab4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x169c>
10002f974:     	mov	w0, #0x1                ; =1
10002f978:     	bl	0x10021a0a4 <_github.com/xgo-dev/llgo/runtime/internal/runtime.AssertNilDeref>
10002f97c:     	mov	x0, xzr
10002f980:     	b	0x10002fab4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x169c>
10002f984:     	mov	w0, #0x28               ; =40
10002f988:     	bl	0x10022ed60 <dyld_stub_binder+0x10022ed60>
10002f98c:     	cbz	x0, 0x10002fbfc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e4>
10002f990:     	mov	x21, x0
10002f994:     	mov	w0, #0x28               ; =40
10002f998:     	bl	0x100215d8c <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002f99c:     	ldr	x8, [x25, #0x8]
10002f9a0:     	adrp	x9, 0x1003f0000 <__MergedGlobals.9998+0xa0>
10002f9a4:     	adrp	x10, 0x1002c9000 <dyld_stub_binder+0x1002c9000>
10002f9a8:     	add	x10, x10, #0xb45
10002f9ac:     	ldr	q0, [x9, #0x940]
10002f9b0:     	adrp	x1, 0x100321000 <__llgo_github.com/cpunion/wasmvm/internal/compiler/ir.BlockID+0x40>
10002f9b4:     	add	x1, x1, #0x660
10002f9b8:     	stp	x8, x10, [x21, #0x10]
10002f9bc:     	mov	w8, #0xe                ; =14
10002f9c0:     	str	q0, [x21]
10002f9c4:     	str	x8, [x21, #0x20]
10002f9c8:     	b	0x10002faa4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x168c>
10002f9cc:     	mov	x1, x0
10002f9d0:     	mov	x0, x20
10002f9d4:     	b	0x10002faac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1694>
10002f9d8:     	and	x0, x0, #0xff
10002f9dc:     	b	0x10002fab4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x169c>
10002f9e0:     	ldr	x21, [sp, #0x48]
10002f9e4:     	mov	w0, #0x8                ; =8
10002f9e8:     	bl	0x10022ed60 <dyld_stub_binder+0x10022ed60>
10002f9ec:     	cbz	x0, 0x10002fbfc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e4>
10002f9f0:     	mov	x22, x0
10002f9f4:     	mov	w0, #0x8                ; =8
10002f9f8:     	bl	0x100215d8c <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002f9fc:     	adrp	x2, 0x100304000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fa00:     	add	x2, x2, #0xa00
10002fa04:     	mov	x0, x25
10002fa08:     	mov	x1, x21
10002fa0c:     	mov	x3, x22
10002fa10:     	bl	0x1001f577c <_errors.As>
10002fa14:     	ldr	x9, [sp, #0x8]
10002fa18:     	tbz	w0, #0x0, 0x10002fa28 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1610>
10002fa1c:     	ldr	x8, [x22]
10002fa20:     	ldr	w9, [x9, #0x50]
10002fa24:     	str	w9, [x8, #0x4]
10002fa28:     	mov	x0, x20
10002fa2c:     	mov	x1, x25
10002fa30:     	b	0x10002faa8 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1690>
10002fa34:     	mov	w25, #0x3               ; =3
10002fa38:     	b	0x10002fa48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002fa3c:     	mov	w25, #0x1               ; =1
10002fa40:     	b	0x10002fa48 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1630>
10002fa44:     	mov	w25, #0x9               ; =9
10002fa48:     	mov	w0, #0x10               ; =16
10002fa4c:     	bl	0x10022ed54 <dyld_stub_binder+0x10022ed54>
10002fa50:     	cbz	x0, 0x10002fbfc <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x17e4>
10002fa54:     	mov	x21, x0
10002fa58:     	mov	w0, #0x10               ; =16
10002fa5c:     	bl	0x100215d8c <_github.com/xgo-dev/llgo/runtime/internal/runtime.recordMemProfileAlloc>
10002fa60:     	ldr	x9, [sp, #0x28]
10002fa64:     	stp	xzr, xzr, [x21]
10002fa68:     	ldr	w0, [x27, #0x4]
10002fa6c:     	ldr	x1, [x9, #0x80]
10002fa70:     	cmp	x1, x0
10002fa74:     	b.ls	0x10002fb04 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16ec>
10002fa78:     	mov	w8, #0xe0               ; =224
10002fa7c:     	ldr	x9, [x9, #0x78]
10002fa80:     	adrp	x1, 0x100304000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fa84:     	add	x1, x1, #0xd68
10002fa88:     	umaddl	x8, w0, w8, x9
10002fa8c:     	ldr	x9, [sp, #0x8]
10002fa90:     	ldr	w9, [x9, #0x50]
10002fa94:     	ldr	x8, [x8, #0x8]
10002fa98:     	strb	w25, [x21]
10002fa9c:     	str	w9, [x21, #0x4]
10002faa0:     	str	x8, [x21, #0x8]
10002faa4:     	mov	x0, x20
10002faa8:     	mov	x2, x21
10002faac:     	bl	0x10002d690 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).fail>
10002fab0:     	mov	w0, #0x3                ; =3
10002fab4:     	mov	x1, x19
10002fab8:     	ldp	x29, x30, [sp, #0x1d0]
10002fabc:     	ldp	x20, x19, [sp, #0x1c0]
10002fac0:     	ldp	x22, x21, [sp, #0x1b0]
10002fac4:     	ldp	x24, x23, [sp, #0x1a0]
10002fac8:     	ldp	x26, x25, [sp, #0x190]
10002facc:     	ldp	x28, x27, [sp, #0x180]
10002fad0:     	add	sp, sp, #0x1e0
10002fad4:     	ret
10002fad8:     	ldr	x2, [sp, #0x80]
10002fadc:     	b	0x10002fae4 <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x16cc>
10002fae0:     	ldur	x2, [x29, #-0x80]
10002fae4:     	mov	x0, x20
10002fae8:     	mov	x1, x8
10002faec:     	b	0x10002faac <_github.com/cpunion/wasmvm/internal/engine/execute.(*Execution).runSlotsLoop+0x1694>
10002faf0:     	mov	x0, x24
10002faf4:     	mov	x1, x22
10002faf8:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fafc:     	mov	x1, x21
10002fb00:     	bl	0x100215e84 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fb04:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb08:     	mov	x1, x22
10002fb0c:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb10:     	mov	x0, x8
10002fb14:     	mov	x1, x21
10002fb18:     	bl	0x100215e84 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fb1c:     	mov	x0, x8
10002fb20:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb24:     	mov	x1, x21
10002fb28:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb2c:     	mov	x0, x8
10002fb30:     	mov	x1, x21
10002fb34:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb38:     	mov	x0, x2
10002fb3c:     	mov	w1, wzr
10002fb40:     	mov	x2, x8
10002fb44:     	mov	w3, #0x2                ; =2
10002fb48:     	bl	0x100216b9c <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fb4c:     	mov	x0, x9
10002fb50:     	mov	w1, #0x1                ; =1
10002fb54:     	mov	w3, #0x2                ; =2
10002fb58:     	bl	0x100216b9c <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fb5c:     	mov	x0, x8
10002fb60:     	mov	w1, #0x1                ; =1
10002fb64:     	mov	x2, x9
10002fb68:     	mov	w3, #0x3                ; =3
10002fb6c:     	bl	0x100216b9c <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fb70:     	mov	x0, x9
10002fb74:     	mov	x1, x21
10002fb78:     	bl	0x1002173fc <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndexU>
10002fb7c:     	mov	w0, #0x1                ; =1
10002fb80:     	bl	0x100215e84 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fb84:     	mov	x0, xzr
10002fb88:     	mov	x1, xzr
10002fb8c:     	bl	0x100215e84 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fb90:     	mov	w0, #0x3                ; =3
10002fb94:     	bl	0x100215e84 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fb98:     	mov	w0, #0x2                ; =2
10002fb9c:     	mov	w1, #0x2                ; =2
10002fba0:     	bl	0x100215e84 <_github.com/xgo-dev/llgo/runtime/internal/runtime.PanicIndex>
10002fba4:     	mov	x0, x25
10002fba8:     	mov	w1, wzr
10002fbac:     	mov	w3, #0x3                ; =3
10002fbb0:     	bl	0x100216b9c <_github.com/xgo-dev/llgo/runtime/internal/runtime.panicBounds>
10002fbb4:     	adrp	x0, 0x100378000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8$in+0x10>
10002fbb8:     	add	x0, x0, #0x890
10002fbbc:     	adrp	x1, 0x100378000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8$in+0x10>
10002fbc0:     	add	x1, x1, #0xb10
10002fbc4:     	bl	0x100215b78 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fbc8:     	brk	#0x1
10002fbcc:     	adrp	x0, 0x100381000 <_*_llgo_github.com/xgo-dev/llgo/runtime/abi.UncommonType+0xa8>
10002fbd0:     	add	x0, x0, #0xfb8
10002fbd4:     	adrp	x1, 0x100304000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fbd8:     	add	x1, x1, #0xa50
10002fbdc:     	bl	0x100215b78 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fbe0:     	brk	#0x1
10002fbe4:     	adrp	x0, 0x100381000 <_*_llgo_github.com/xgo-dev/llgo/runtime/abi.UncommonType+0xa8>
10002fbe8:     	add	x0, x0, #0xfb8
10002fbec:     	adrp	x1, 0x100304000 <_github.com/cpunion/wasmvm/internal/engine/execute.struct$P8EgrRL2hi3L6nwyPFF1FRuCTbMu0NbpU0A2JbtiBGU$fields+0x148>
10002fbf0:     	add	x1, x1, #0xa60
10002fbf4:     	bl	0x100215b78 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fbf8:     	brk	#0x1
10002fbfc:     	adrp	x0, 0x100381000 <_*_llgo_github.com/xgo-dev/llgo/runtime/abi.UncommonType+0xa8>
10002fc00:     	add	x0, x0, #0xfb8
10002fc04:     	adrp	x1, 0x100378000 <__llgo_func$0MAhD-4Wo_spr87bqPaEJkBw5MFRclWcO_B99CB4cG8$in+0x10>
10002fc08:     	add	x1, x1, #0x880
10002fc0c:     	bl	0x100215b78 <_github.com/xgo-dev/llgo/runtime/internal/runtime.Panic>
10002fc10:     	brk	#0x1
