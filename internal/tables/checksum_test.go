// SPDX-License-Identifier: LGPL-2.1-or-later
package tables

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// tableChecksums pins the SHA-256 of every table's canonical serialization
// (porting-guide.md table strategy: a unit test embeds checksums of the
// generated data to catch accidental edits). Regenerating from the pinned
// FFmpeg tree must reproduce these sums exactly; any other change to
// tables_gen.go or the init code is an error.
var tableChecksums = map[string]string{
	"CBInMap":            "f2edcd9cfa29ba24597f583600a9da0443340fd0f5637ed4ce909e87e022c832",
	"CBMaxval":           "980ae7df01eaa726b4debeb5fa9b728c227d55ac305652343aabb0762434cb09",
	"CBOutMap":           "1f594a991528298708c8f529888481865f81615dbf83232cdf86f568d55cd10f",
	"CBRange":            "5c46c5adbfc62c9249ca6bf1eabcc9b832c06473ccd2d8859df61d26151e7a2e",
	"ChanConfigs":        "10a2d5db947a4c7eea9181474f70ade312124df57b15ac1621752cf995f5895e",
	"ChanMaps":           "7116ebeb19bf9489fdeae6b4438fe9eb25e1363be2eb974e4f456147653e5915",
	"CodebookVectorIdx":  "bfe4becafe124fd40b9b547c27351f3ce51cb2a933f2436c6cb1b8ef317c2a5c",
	"CodebookVectorVals": "d152bc14bf01fcc70906bcd0860b0a40bc9d1d1131a830041fa7d34bfad745a9",
	"CodebookVectors":    "7c02b21db6564877c871a70a572e03c94e1592c5d75453643683b61809c3291e",
	"MaxvalCB":           "737fa1d04c6a5956cb2a2c3ac6c128f0038127d1963f53727fc83ca83da97506",
	"NumSwb1024":         "7082fa290d37e9dd77457ab287cd4c49349daf34c53514eb4ee59ab6a2fa3062",
	"NumSwb128":          "cc3812369da5c33577f2ec30a4e01cda8b6f6322d4856a63b7725fb2f4fcd511",
	pow2SFName:           "5912db81f7ab175f90291ef11f464ff95ca48fd6d19f7438216adc9c91800738",
	pow34SFName:          "40c3023f4193f334884e4c9d2510658cbebf7a45a820a92ef57ee0a65194b692",
	"PsyABRMap":          "808aa39842bf651b7043e3358b2849eb37162997f20ee345130419bf73a0124a",
	"PsyFirCoeffs":       "694155afb49ceaa4a4b4ec6c9e525486f3b416ec2ad74ed0bdf4feec9c6ec3ef",
	"PsyVBRMap":          "f3a3fb6ba539dfebae602859a481578def32f396671216a1735f42bbf347dda8",
	"RunValueBitsLong":   "f7bcc379d9b24b597d262543dc714439940c97a53fbed78e220fc3d942890e4c",
	"RunValueBitsShort":  "3bedf03eb184b6da50dfcd7e69b147395ad889c7726a39226b0ab0f48b13f68d",
	"ScalefactorBits":    "a60374a3ad10e1086ad9f5ccef623ac8bbc0e5636dd367b949b0342216835ed8",
	"ScalefactorCode":    "62f0b1bb9be62f6279adfe3bd7636d2ea22268cfe8d09a6a41526f1bcad0bb36",
	"SpectralBits":       "b4e9d1d2c5ae1693863f35d573634bb031fbb1db0a2fb3fc3b41a69c4a22fa39",
	"SpectralCodes":      "d3f66d76cb273a2427969512754b2da36b27f26d8086e3dfd981e9fc5c869f23",
	"SwbOffset1024":      "bc9985ddfa44ad8a6329e366092ed82f7de74ca505ee8d29263a25f81b3d407b",
	"SwbOffset128":       "aa2ba1b2c222892a4a86fdcdf0e01fab3ddcbbacade5346a29974d752341e94d",
	"SwbSize1024":        "8ba3f00e0e3c0927e2386cad03bbd2e21a6f89d1b86e24e35e328cb904af5dfe",
	"SwbSize128":         "b6a67a5108efff92499bd00edaff55d2ebb3bdc0f84e6a0ea50b42285b27ba76",
	"TNSMaxBands1024":    "a915c41a5171ca71be12edd26e3774aab3af3a96ef520503ed7066462c1682c7",
	"TNSMaxBands128":     "160832cfdb3ca62e6cd95825db5901201b0a07a8b708b8cd8ed03d792fcacfe3",
	"TNSMinSfbLong":      "2076086d2abe6aa1378a724b53086ae05ea62db5be77489bdf8d29d5391e092e",
	"TNSMinSfbShort":     "70e13d330293b23cf871206765485668470ebe074812a007dd9ed5ca3846f96f",
	"TNSTmp2Map":         "f95daa47a5db5f6f30fa600e20005c87372d85bba95ad18f74bf38eba469517e",
	"WindowGrouping":     "6ba91b3387f59afaeab1d4aad2c5f1b5ee9fb1543e6cf7c439b14c355ad3654f",
}

func TestTableChecksums(t *testing.T) {
	got := serializedTables()
	if len(got) != len(tableChecksums) {
		t.Fatalf("have %d tables, want %d checksums", len(got), len(tableChecksums))
	}
	for name, want := range tableChecksums {
		data, ok := got[name]
		if !ok {
			t.Errorf("%s: no such table", name)
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			t.Errorf("%s: checksum %x, want %s (table edited without regeneration?)",
				name, sum, want)
		}
	}
}
