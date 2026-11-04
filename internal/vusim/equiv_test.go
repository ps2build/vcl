package vusim

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func randomMem(vm *VM, seed int64) {
	r := rand.New(rand.NewSource(seed))
	for q := range vm.Mem {
		for l := 0; l < 4; l++ {
			f := math.Float32bits(float32(r.Float64()*4 - 2))
			vm.Mem[q][l] = f&^0xffff | uint32(r.Intn(5))
		}
	}
	for i := 1; i < 16; i++ {
		vm.VI[i] = uint16(r.Intn(6))
	}
	if os.Getenv("VUSIM_ZEROREGS") == "" {
		for i := 1; i < 32; i++ {
			for l := 0; l < 4; l++ {
				vm.VF[i][l] = float32(r.Float64()*4 - 2)
			}
		}
	}
	vm.Q, vm.I = 1, 1
}

func runFile(t *testing.T, path string, seed int64) *VM {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	vm, err := New(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	randomMem(vm, seed)
	if tv := os.Getenv("VUSIM_TRACE"); tv != "" && strings.HasSuffix(path, os.Getenv("VUSIM_TRACEFILE")) && seed == 1 {
		fmt.Sscan(tv, &vm.TraceVF)
	}
	if os.Getenv("VUSIM_LOG") != "" {
		vm.Log = map[int][]string{}
	}
	vm.Done = vm.Run(300000, 3)
	if e := vm.Entry(os.Getenv("VUSIM_ENTRY")); e >= 0 {
		vm.Done = vm.RunAt(e, 400000, 3)
	}
	return vm
}

func TestEquivalence(t *testing.T) {
	a, b := os.Getenv("VUSIM_A"), os.Getenv("VUSIM_B")
	if a == "" || b == "" {
		t.Skip("VUSIM_A and VUSIM_B not set")
	}
	skipped, compared := 0, 0
	defer func() { t.Logf("compared %d skipped %d", compared, skipped) }()
	for seed := int64(1); seed <= 12; seed++ {
		va, vb := runFile(t, filepath.Clean(a), seed), runFile(t, filepath.Clean(b), seed)
		if va.Err != nil || vb.Err != nil {
			t.Fatalf("errors: %v / %v", va.Err, vb.Err)
		}
		if !va.Done || !vb.Done {
			skipped++
			continue
		}
		compared++
		if seed <= 3 {
			t.Logf("seed %d: kicks %d/%d cycles %d/%d", seed, len(va.Kicks), len(vb.Kicks), va.Cycle, vb.Cycle)
		}
		if len(va.Kicks) != len(vb.Kicks) {
			t.Errorf("seed %d: kicks %d vs %d (cycles %d/%d)", seed, len(va.Kicks), len(vb.Kicks), va.Cycle, vb.Cycle)
			continue
		}
		for i := range va.Kicks {
			if va.Kicks[i].Addr != vb.Kicks[i].Addr || va.Kicks[i].Mem != vb.Kicks[i].Mem {
				d := 0
				for q := range va.Kicks[i].Mem {
					if va.Kicks[i].Mem[q] != vb.Kicks[i].Mem[q] && d < 4 {
						t.Logf("  quad %d: %08x vs %08x", q, va.Kicks[i].Mem[q], vb.Kicks[i].Mem[q])
						d++
					}
				}
				t.Errorf("seed %d kick %d differs (addr %d/%d)", seed, i, va.Kicks[i].Addr, vb.Kicks[i].Addr)
				if va.Log != nil {
					for _, q := range []int{308} {
						t.Logf("A %d: %v", q, va.Log[q])
						t.Logf("B %d: %v", q, vb.Log[q])
					}
				}
				break
			}
		}
		if len(va.Kicks) == 0 && va.Mem != vb.Mem {
			d := 0
			for q := range va.Mem {
				if va.Mem[q] != vb.Mem[q] && d < 6 {
					t.Logf("  quad %d: %08x vs %08x", q, va.Mem[q], vb.Mem[q])
					d++
				}
			}
			if va.Log != nil {
				for _, q := range []int{308} {
					t.Logf("A%d: %v", q, va.Log[q])
					t.Logf("B%d: %v", q, vb.Log[q])
				}
				t.Logf("A513: %v", va.Log[513])
				t.Logf("B513: %v", vb.Log[513])
			}
			t.Errorf("seed %d: final memory differs", seed)
		}
	}
}
