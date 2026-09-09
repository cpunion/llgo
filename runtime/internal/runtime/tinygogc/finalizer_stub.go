//go:build baremetal && !nogc

package tinygogc

func preserveFinalizableObjects() {}

func scheduleFinalizers() {}

func noteFinalizerReference(uintptr) {}

func beginFinalizerDebugRootScan() {}

func endFinalizerDebugRootScan() {}

func noteFinalizerDebugRoot(uintptr, uintptr) {}
