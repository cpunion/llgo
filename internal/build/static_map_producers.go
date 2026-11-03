package build

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/xgo-dev/llgo/cl"
)

const staticMapInitFragmentVersion = 1
const maxStaticMapInitFragmentBytes = 16 << 20

// Producer receipts remain independent of consumer reachability. They bind
// canonical retained constructors to the exact archive, source/configuration
// fingerprint and compiler executable. They never carry omission decisions.
type staticMapInitFragment struct {
	Version         int
	PackageIdentity string
	Fingerprint     string
	CompilerSHA256  string
	ArchiveSHA256   string
	Producers       []cl.StaticMapInitProducer
}

func (c *context) staticMapInitGatesEnabled() bool {
	return c != nil && c.frontendOptions.CanonicalStaticMapInits
}

func (c *context) staticMapInitCompilerIdentity() (string, error) {
	c.staticMapCompilerOnce.Do(func() {
		path, err := os.Executable()
		if err == nil {
			c.staticMapCompilerSHA256, err = digestFile(path)
		}
		c.staticMapCompilerError = err
	})
	return c.staticMapCompilerSHA256, c.staticMapCompilerError
}

func captureStaticMapInitProducers(pkg *aPackage) error {
	if pkg == nil || pkg.Package == nil || pkg.LPkg == nil {
		return fmt.Errorf("static map producer capture requires a package module")
	}
	producers, err := cl.StaticMapInitProducers(pkg.LPkg)
	if err != nil {
		return err
	}
	identity := coroRawPackageIdentity(pkg)
	for _, producer := range producers {
		if producer.PackageIdentity != identity {
			return fmt.Errorf("static map producer belongs to a different package")
		}
	}
	pkg.StaticMapInitProducers = producers
	pkg.StaticMapInitCaptured = true
	return nil
}

func staticMapInitDigestValid(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == value
}

func validateStaticMapInitFragment(fragment *staticMapInitFragment) error {
	if fragment.Version != staticMapInitFragmentVersion || fragment.PackageIdentity == "" ||
		!staticMapInitDigestValid(fragment.Fingerprint) || !staticMapInitDigestValid(fragment.CompilerSHA256) ||
		!staticMapInitDigestValid(fragment.ArchiveSHA256) || fragment.Producers == nil {
		return fmt.Errorf("invalid static map producer fragment binding")
	}
	previous := ""
	for _, producer := range fragment.Producers {
		if err := cl.ValidateStaticMapInitProducer(producer); err != nil {
			return err
		}
		if producer.PackageIdentity != fragment.PackageIdentity || producer.GateSymbol <= previous {
			return fmt.Errorf("static map producers have a different owner, duplicates or noncanonical order")
		}
		previous = producer.GateSymbol
	}
	return nil
}

func encodeStaticMapInitFragment(pkg *aPackage, compilerSHA256, archive string) ([]byte, error) {
	if pkg == nil || pkg.Package == nil || !pkg.StaticMapInitCaptured {
		return nil, fmt.Errorf("static map producer capture is missing")
	}
	archiveSHA256, err := digestFile(archive)
	if err != nil {
		return nil, err
	}
	producers := append([]cl.StaticMapInitProducer{}, pkg.StaticMapInitProducers...)
	sort.Slice(producers, func(i, j int) bool { return producers[i].GateSymbol < producers[j].GateSymbol })
	fragment := staticMapInitFragment{
		Version: staticMapInitFragmentVersion, PackageIdentity: coroRawPackageIdentity(pkg),
		Fingerprint: pkg.Fingerprint, CompilerSHA256: compilerSHA256,
		ArchiveSHA256: archiveSHA256, Producers: producers,
	}
	if err := validateStaticMapInitFragment(&fragment); err != nil {
		return nil, err
	}
	data, err := json.Marshal(fragment)
	if err == nil && len(data) > maxStaticMapInitFragmentBytes {
		err = fmt.Errorf("oversized static map producer fragment")
	}
	return data, err
}

func decodeStaticMapInitFragment(data []byte) (*staticMapInitFragment, error) {
	var fragment staticMapInitFragment
	if len(data) > maxStaticMapInitFragmentBytes || json.Unmarshal(data, &fragment) != nil {
		return nil, fmt.Errorf("invalid static map producer fragment")
	}
	canonical, err := json.Marshal(fragment)
	if err != nil || !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("noncanonical static map producer fragment")
	}
	if err := validateStaticMapInitFragment(&fragment); err != nil {
		return nil, err
	}
	return &fragment, nil
}

func readCachedStaticMapInitProducers(path, expectedHash, archive, identity, fingerprint, compilerSHA256 string) (*staticMapInitFragment, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStaticMapInitFragmentBytes+1))
	if err != nil {
		return nil, err
	}
	if !staticMapInitDigestValid(expectedHash) || digestBytes(data) != expectedHash {
		return nil, fmt.Errorf("static map producer checksum mismatch")
	}
	fragment, err := decodeStaticMapInitFragment(data)
	if err != nil {
		return nil, err
	}
	if fragment.PackageIdentity != identity || fragment.Fingerprint != fingerprint || fragment.CompilerSHA256 != compilerSHA256 {
		return nil, fmt.Errorf("static map producer identity or configuration mismatch")
	}
	archiveSHA256, err := digestFile(archive)
	if err != nil {
		return nil, err
	}
	if fragment.ArchiveSHA256 != archiveSHA256 {
		return nil, fmt.Errorf("static map producer archive mismatch")
	}
	return fragment, nil
}
