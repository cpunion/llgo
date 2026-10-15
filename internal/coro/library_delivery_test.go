package coro

import (
	"bytes"
	"strings"
	"testing"
)

func deliveryLibrarySummaries() ([]LibraryEffectSummary, DeliveryRecipeRecord) {
	record, _, _, _ := deliveryTestInputs()
	producer := LibraryEffectFunction{
		ID: record.Producer, ABIHash: record.ProducerABI,
		Effect: MayPark.Join(AwaitStructured), Exec: MayUnwind | NeedsCleanupFrame,
		FuncRep: DirectCoro, Primary: PrimaryCoroutine, ManagedEntry: ManagedEntryCoroutine,
		PrimarySymbol: "api.acquire$coro", Delivery: record,
	}
	initializer := LibraryEffectFunction{
		ID: record.Initializer, ABIHash: record.InitializerABI,
		FuncRep: DirectPlain, Primary: PrimaryPlain, ManagedEntry: ManagedEntryPlain,
		PrimarySymbol: "cleanup.initialize",
	}
	drop := LibraryEffectFunction{
		ID: record.Drop, ABIHash: record.DropABI,
		Effect: MayPark.Join(AwaitStructured), Exec: MayUnwind,
		FuncRep: DirectCoro, Primary: PrimaryCoroutine, ManagedEntry: ManagedEntryCoroutine,
		PrimarySymbol: "cleanup.drop$coro",
	}
	return []LibraryEffectSummary{
		{Schema: LibraryEffectSummarySchema, Package: "example/api", Metadata: testLibraryEffectMetadata(), Functions: []LibraryEffectFunction{producer}},
		{Schema: LibraryEffectSummarySchema, Package: "example/cleanup", Metadata: testLibraryEffectMetadata(), Functions: []LibraryEffectFunction{initializer, drop}},
	}, record
}

func TestLibraryDeliveryRecordCrossPackageConsumption(t *testing.T) {
	summaries, record := deliveryLibrarySummaries()
	var section []byte
	for _, summary := range summaries {
		data, err := summary.MarshalRecord()
		if err != nil {
			t.Fatal(err)
		}
		section = append(section, data...)
	}
	decoded, err := ParseLibraryEffectSummaryRecords(section)
	if err != nil {
		t.Fatal(err)
	}
	index, err := NewLibraryEffectIndex(decoded, testLibraryEffectMetadata())
	if err != nil {
		t.Fatal(err)
	}
	got, found := index.LookupDelivery(record.Producer)
	if !found || got != record {
		t.Fatalf("delivery=%+v present=%t", got, found)
	}
	got.Drop = "llgo.function.v0:mutated"
	if again, _ := index.LookupDelivery(record.Producer); again != record {
		t.Fatal("lookup changed the immutable producer contract")
	}
	if absent, found := index.LookupDelivery(record.Drop); found || !absent.IsZero() {
		t.Fatal("ordinary helper acquired resource delivery")
	}
}

func TestLibraryDeliveryRejectsMissingOrWrongHelper(t *testing.T) {
	for name, change := range map[string]func([]LibraryEffectSummary){
		"missing-initializer": func(s []LibraryEffectSummary) { s[1].Functions = s[1].Functions[1:] },
		"missing-drop":        func(s []LibraryEffectSummary) { s[1].Functions = s[1].Functions[:1] },
		"wrong-init-abi":      func(s []LibraryEffectSummary) { s[1].Functions[0].ABIHash = strings.Repeat("4", 64) },
		"wrong-drop-abi":      func(s []LibraryEffectSummary) { s[1].Functions[1].ABIHash = strings.Repeat("4", 64) },
		"wrong-producer":      func(s []LibraryEffectSummary) { s[0].Functions[0].Delivery.Producer = "llgo.function.v0:other" },
		"wrong-producer-abi": func(s []LibraryEffectSummary) {
			s[0].Functions[0].Delivery.ProducerABI = strings.Repeat("4", 64)
		},
		"wrong-version": func(s []LibraryEffectSummary) { s[0].Functions[0].Delivery.Version++ },
	} {
		t.Run(name, func(t *testing.T) {
			summaries, _ := deliveryLibrarySummaries()
			change(summaries)
			if _, err := NewLibraryEffectIndex(summaries, testLibraryEffectMetadata()); err == nil {
				t.Fatal("accepted incomplete or incompatible delivery metadata")
			}
		})
	}
}

func TestLibraryDeliveryUsesTargetLayoutBound(t *testing.T) {
	summaries, _ := deliveryLibrarySummaries()
	metadata := testLibraryEffectMetadata()
	metadata.PointerBits = 32
	metadata.TargetTriple = "wasm32-unknown-wasip1"
	metadata.DataLayout = "e-p:32:32"
	for index := range summaries {
		summaries[index].Metadata = metadata
	}
	summaries[0].Functions[0].Delivery.ResultSize = uint64(1) << 32
	if _, err := NewLibraryEffectIndex(summaries, metadata); err == nil {
		t.Fatal("accepted a result buffer that cannot fit the target address space")
	}
}

func TestLibraryDeliveryAbsenceAndVersionAreExplicit(t *testing.T) {
	summary := testLibraryEffectSummary(t, "example/ordinary", false)
	data, err := summary.MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"delivery"`)) {
		t.Fatal("ordinary functions acquired resource metadata")
	}
	index, err := NewLibraryEffectIndex([]LibraryEffectSummary{summary}, summary.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if record, found := index.LookupDelivery(summary.Functions[0].ID); found || !record.IsZero() {
		t.Fatal("absent producer contract was inferred from an ordinary callable")
	}
	summary.Schema = "llgo.coro.library-effect-summary.v16"
	if _, err := summary.MarshalStable(); err == nil {
		t.Fatal("accepted the preceding schema as an implicit nonresource contract")
	}
}
