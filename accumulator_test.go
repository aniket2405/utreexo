package utreexo

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func (p *Pollard) posMapSanity() error {
	for mHash, node := range p.nodeMap {
		if node == nil {
			return fmt.Errorf("Node in nodemap is nil. Key: %s",
				hex.EncodeToString(mHash[:]))
		}

		pos := p.calculatePosition(node)
		gotNode, _, _, err := p.getNode(pos)
		if err != nil {
			return err
		}

		if gotNode == nil {
			return fmt.Errorf("Couldn't fetch pos %d, expected %s",
				pos, hex.EncodeToString(node.data[:]))
		}

		if gotNode.data != node.data {
			return fmt.Errorf("Calculated pos %d for node %s but read %s",
				pos, hex.EncodeToString(node.data[:]),
				hex.EncodeToString(gotNode.data[:]))
		}
	}

	return nil
}

func TestUndo(t *testing.T) {
	t.Parallel()

	var tests = []struct {
		startLeafCount int
		dels           []Hash
		adds           []Leaf
	}{
		{
			6,
			[]Hash{{6}, {4}, {2}, {1}, {3}},
			[]Leaf{{Hash: Hash{7}}, {Hash: Hash{8}}},
		},
		{
			8,
			[]Hash{{5}, {6}},
			nil,
		},
		{
			8,
			[]Hash{{4}, {5}},
			nil,
		},
		{
			8,
			nil,
			[]Leaf{{Hash: Hash{9}}, {Hash: Hash{10}}},
		},
		{
			8,
			[]Hash{{4}, {5}},
			[]Leaf{{Hash: Hash{9}}, {Hash: Hash{10}}},
		},
		{
			8,
			[]Hash{{2}, {3}, {7}},
			[]Leaf{{Hash: Hash{9}}, {Hash: Hash{10}}},
		},
		{
			7,
			[]Hash{{5}, {6}},
			[]Leaf{{Hash: Hash{8}}, {Hash: Hash{9}}},
		},

		{
			12,
			nil,
			[]Leaf{{Hash: Hash{14}}, {Hash: Hash{15}}, {Hash: Hash{16}}, {Hash: Hash{17}}},
		},
	}

	for _, test := range tests {
		p := NewAccumulator(true)

		adds := make([]Leaf, test.startLeafCount)
		for i := range adds {
			adds[i].Hash[0] = uint8(i + 1)
		}

		// Create the initial starting off pollard.
		err := p.Modify(adds, nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		beforeRoots := p.GetRoots()

		bp, err := p.Prove(test.dels)
		if err != nil {
			t.Fatal(err)
		}

		err = proofSanity(bp)
		if err != nil {
			t.Fatal(err)
		}

		beforeStr := p.String()

		// Perform the modify to undo.
		err = p.Modify(test.adds, test.dels, bp.Targets)
		if err != nil {
			t.Fatal(err)
		}
		afterStr := p.String()

		err = p.posMapSanity()
		if err != nil {
			str := fmt.Errorf("TestUndo fail: error %v"+
				"\nbefore:\n\n%s"+
				"\nafter:\n\n%s",
				err,
				beforeStr,
				afterStr)
			t.Fatal(str)
		}

		err = p.checkHashes()
		if err != nil {
			str := fmt.Errorf("TestUndo fail: error %v"+
				"\nbefore:\n\n%s"+
				"\nafter:\n\n%s",
				err,
				beforeStr,
				afterStr)
			t.Fatal(str)
		}

		// Perform the undo.
		err = p.Undo(uint64(len(test.adds)), bp.Targets, test.dels)
		if err != nil {
			t.Fatal(err)
		}
		undoStr := p.String()

		err = p.checkHashes()
		if err != nil {
			err := fmt.Errorf("TestUndo fail: error %v"+
				"\nbefore:\n\n%s"+
				"\nafter:\n\n%s"+
				"\nundo:\n\n%s",
				err,
				beforeStr,
				afterStr,
				undoStr)
			t.Fatal(err)
		}
		if uint64(len(p.nodeMap)) != p.numLeaves-p.numDels {
			err := fmt.Errorf("TestUndo fail have %d leaves in map but only %d leaves in total",
				len(p.nodeMap), p.numLeaves-p.numDels)
			t.Fatal(err)
		}
		err = p.posMapSanity()
		if err != nil {
			err := fmt.Errorf("TestUndo fail: error %v"+
				"\nbefore:\n\n%s"+
				"\nafter:\n\n%s"+
				"\nundo:\n\n%s",
				err,
				beforeStr,
				afterStr,
				undoStr)
			t.Fatal(err)
		}

		afterRoots := p.GetRoots()

		if !reflect.DeepEqual(beforeRoots, afterRoots) {
			beforeStr := printHashes(beforeRoots)
			afterStr := printHashes(afterRoots)

			err := fmt.Errorf("TestUndo Fail: roots don't equal, before %v, after %v",
				beforeStr, afterStr)
			t.Fatal(err)
		}
	}
}

func TestRandUndo(t *testing.T) {
	t.Parallel()

	p := NewAccumulator(true)

	sc := newSimChain(0x07)
	numAdds := uint32(5)
	for b := 0; b <= 1000; b++ {
		adds, durations, delHashes := sc.NextBlock(numAdds)

		bp, err := p.Prove(delHashes)
		if err != nil {
			t.Fatalf("TestPollardUndoRand fail at block %d. Error: %v", b, err)
		}
		undoTargs := make([]uint64, len(bp.Targets))
		copy(undoTargs, bp.Targets)

		undoDelHashes := make([]Hash, len(delHashes))
		copy(undoDelHashes, delHashes)

		err = p.Verify(delHashes, bp)
		if err != nil {
			t.Fatal(err)
		}

		// We'll be comparing 3 things. Roots, nodeMap and leaf count.
		beforeRoot := p.GetRoots()
		beforeMap := make(map[miniHash]polNode)
		for key, value := range p.nodeMap {
			beforeMap[key] = *value
		}
		beforeLeaves := p.numLeaves

		err = p.Modify(adds, delHashes, bp.Targets)
		if err != nil {
			t.Fatalf("TestRandUndo fail at block %d. Error: %v", b, err)
		}

		if p.numLeaves-uint64(len(adds)) != beforeLeaves {
			err := fmt.Errorf("TestRandUndo fail at block %d. "+
				"Added %d leaves but have %d leaves after modify",
				b, len(adds), p.numLeaves)
			t.Fatal(err)
		}

		// Undo the last modify.
		if b%3 == 0 {
			err := p.Undo(uint64(len(adds)), undoTargs, undoDelHashes)
			if err != nil {
				t.Fatal(err)
			}

			sc.BackOne(adds, durations, delHashes)
			afterRoot := p.GetRoots()
			if !reflect.DeepEqual(beforeRoot, afterRoot) {
				err := fmt.Errorf("TestRandUndo fail at block %d, "+
					"root mismatch. Before %s, after %s",
					b, printHashes(beforeRoot), printHashes(afterRoot))
				t.Fatal(err)
			}

			if len(p.nodeMap) != len(beforeMap) {
				err := fmt.Errorf("TestRandUndo fail at block %d, map length mismatch. "+
					"before %d, after %d", b, len(beforeMap), len(p.nodeMap))
				t.Fatal(err)
			}

			for key, value := range beforeMap {
				node, found := p.nodeMap[key]
				if !found {
					err := fmt.Errorf("TestRandUndo fail at block %d, hash %s not found after undo",
						b, hex.EncodeToString(key[:]))
					t.Fatal(err)
				}

				if node.data != value.data {
					err := fmt.Errorf("TestRandUndo fail at block %d, for hash %s, expected %s, got %s ",
						b, hex.EncodeToString(key[:]),
						hex.EncodeToString(value.data[:]),
						hex.EncodeToString(node.data[:]))
					t.Fatal(err)
				}
			}
		}

		// Check hashes.
		if b%500 == 0 {
			for _, root := range p.roots {
				if root.lNiece != nil && root.rNiece != nil {
					err = checkHashes(root.lNiece, root.rNiece, &p)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if uint64(len(p.nodeMap)) != p.numLeaves-p.numDels {
			err := fmt.Errorf("TestUndoRand fail at block %d: "+
				"have %d leaves in map but only %d leaves in total",
				b, len(p.nodeMap), p.numLeaves-p.numDels)
			t.Fatal(err)
		}

		err = p.posMapSanity()
		if err != nil {
			err := fmt.Errorf("TestUndoRand fail at block %d: error %v", b, err)
			t.Fatal(err)
		}
	}
}

// checkHashes moves down the tree and calculates the parent hash from the children.
// It errors if the calculated hash doesn't match the hash found in the pollard.
func checkHashes(node, sibling *polNode, p *Pollard) error {
	// If node has a niece, then we can calculate the hash of the sibling because
	// every tree is a perfect binary tree.
	if node.lNiece != nil {
		calculated := parentHash(node.lNiece.data, node.rNiece.data)
		if sibling.data != calculated {
			return fmt.Errorf("For position %d, calculated %s from left %s, right %s but read %s",
				p.calculatePosition(sibling),
				hex.EncodeToString(calculated[:]),
				hex.EncodeToString(node.lNiece.data[:]), hex.EncodeToString(node.rNiece.data[:]),
				hex.EncodeToString(sibling.data[:]))
		}

		err := checkHashes(node.lNiece, node.rNiece, p)
		if err != nil {
			return err
		}
	}

	if sibling.lNiece != nil {
		calculated := parentHash(sibling.lNiece.data, sibling.rNiece.data)
		if node.data != calculated {
			return fmt.Errorf("For position %d, calculated %s from left %s, right %s but read %s",
				p.calculatePosition(node),
				hex.EncodeToString(calculated[:]),
				hex.EncodeToString(sibling.lNiece.data[:]), hex.EncodeToString(sibling.rNiece.data[:]),
				hex.EncodeToString(node.data[:]))
		}

		err := checkHashes(sibling.lNiece, sibling.rNiece, p)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkHashes is a wrapper around the checkHashes function. Provides an easy function to
// check that the pollard has correct hashes.
func (p *Pollard) checkHashes() error {
	for _, root := range p.roots {
		if root.lNiece != nil && root.rNiece != nil {
			// First check the root hash.
			calculatedHash := parentHash(root.lNiece.data, root.rNiece.data)
			if calculatedHash != root.data {
				err := fmt.Errorf("For position %d, calculated %s from left %s, right %s but read %s",
					p.calculatePosition(root),
					hex.EncodeToString(calculatedHash[:]),
					hex.EncodeToString(root.lNiece.data[:]), hex.EncodeToString(root.rNiece.data[:]),
					hex.EncodeToString(root.data[:]))
				return err
			}

			// Then check all other hashes.
			err := checkHashes(root.lNiece, root.rNiece, p)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// positionSanity tries to grab all the eligible positions of the pollard and
// calculates its position. Returns an error if the position calculated does
// not match the position used to fetch the node.
func (p *Pollard) positionSanity() error {
	totalRows := treeRows(p.numLeaves)

	for row := uint8(0); row < totalRows; row++ {
		pos := startPositionAtRow(row, totalRows)
		maxPosAtRow, err := maxPositionAtRow(row, totalRows, p.numLeaves)
		if err != nil {
			return fmt.Errorf("positionSanity fail. Error %v", err)
		}

		for pos < maxPosAtRow {
			node, _, _, err := p.getNode(pos)
			if err != nil {
				return fmt.Errorf("positionSanity fail. Error %v", err)
			}

			if node != nil {
				gotPos := p.calculatePosition(node)

				if gotPos != pos {
					err := fmt.Errorf("expected %d but got %d for. Node: %s",
						pos, gotPos, node.String())
					return fmt.Errorf("positionSanity fail. Error %v", err)
				}
			}

			pos++
		}
	}

	return nil
}

// simChain is for testing; it spits out "blocks" of adds and deletes
type simChain struct {
	ttlSlices    [][]Hash
	blockHeight  int32
	leafCounter  uint64
	durationMask uint32
	lookahead    int32
	rnd          *rand.Rand
}

// newSimChain initializes and returns a simchain
func newSimChain(duration uint32) *simChain {
	var s simChain
	s.blockHeight = -1
	s.durationMask = duration
	s.ttlSlices = make([][]Hash, s.durationMask+1)
	s.rnd = rand.New(rand.NewSource(0))
	return &s
}

// newSimChainWithSeed initializes and returns a simchain, with an externally supplied seed
func newSimChainWithSeed(duration uint32, seed int64) *simChain {
	var s simChain
	s.blockHeight = -1
	s.durationMask = duration
	s.ttlSlices = make([][]Hash, s.durationMask+1)
	s.rnd = rand.New(rand.NewSource(seed))
	return &s
}

// BackOne takes the output of NextBlock and undoes the block
func (s *simChain) BackOne(leaves []Leaf, durations []int32, dels []Hash) {

	// push in the deleted hashes on the left, trim the rightmost
	s.ttlSlices = append([][]Hash{dels}, s.ttlSlices[:len(s.ttlSlices)-1]...)

	// Gotta go through the leaves and delete them all from the ttlslices
	for i := range leaves {
		if durations[i] == 0 {
			continue
		}
		s.ttlSlices[durations[i]] =
			s.ttlSlices[durations[i]][:len(s.ttlSlices[durations[i]])-1]
	}

	s.blockHeight--
}

// NextBlock outputs a new simulation block given the additions for the block
// to be outputed
func (s *simChain) NextBlock(numAdds uint32) ([]Leaf, []int32, []Hash) {
	s.blockHeight++

	if s.blockHeight == 0 && numAdds == 0 {
		numAdds = 1
	}
	// they're all forgettable
	adds := make([]Leaf, numAdds)
	durations := make([]int32, numAdds)

	// make dels; dels are preset by the ttlMap
	delHashes := s.ttlSlices[0]
	s.ttlSlices = append(s.ttlSlices[1:], []Hash{})

	// make a bunch of unique adds & make an expiry time and add em to
	// the TTL map
	for j := range adds {
		adds[j].Hash[0] = uint8(s.leafCounter)
		adds[j].Hash[1] = uint8(s.leafCounter >> 8)
		adds[j].Hash[2] = uint8(s.leafCounter >> 16)
		adds[j].Hash[3] = 0xff
		adds[j].Hash[4] = uint8(s.leafCounter >> 24)
		adds[j].Hash[5] = uint8(s.leafCounter >> 32)

		durations[j] = int32(s.rnd.Uint32() & s.durationMask)

		// with "+1", the duration is 1 to 256, so the forest never gets
		// big or tall.  Without the +1, the duration is sometimes 0,
		// which makes a leaf last forever, and the forest will expand
		// over time.

		// the first utxo added lives forever.
		// (prevents leaves from going to 0 which is buggy)
		if s.blockHeight == 0 {
			durations[j] = 0
		}

		if durations[j] != 0 && durations[j] < s.lookahead {
			adds[j].Remember = true
		}

		if durations[j] != 0 {
			s.ttlSlices[durations[j]-1] =
				append(s.ttlSlices[durations[j]-1], adds[j].Hash)
		}

		s.leafCounter++
	}

	return adds, durations, delHashes
}

// getAddsAndDels generates leaves to add and then randomly grabs some of those
// leaves to be deleted.
//
// NOTE if getAddsAndDels are called multiple times for the same pollard, pass in
// p.numLeaves into getAddsAndDels after the pollard has been modified with the
// previous set of adds and deletions. The leaves genereated are not random and
// are just the next leaf encoded to a 32 byte hash.
func getAddsAndDels(currentLeaves, addCount, delCount uint32) ([]Leaf, []Hash, []uint64) {
	if addCount == 0 {
		return nil, nil, nil
	}
	leaves := make([]Leaf, addCount)
	for i := uint32(0); i < addCount; i++ {
		// Convert int to byte slice.
		bs := make([]byte, 32)
		hashInt := i + currentLeaves
		binary.LittleEndian.PutUint32(bs, hashInt)

		// Add FF at the end as you can't add an empty leaf to the accumulator.
		bs[31] = 0xFF

		// Hash the byte slice.
		leaves[i] = Leaf{Hash: *(*Hash)(bs)}
	}

	delHashes := make([]Hash, delCount)
	delTargets := make([]uint64, delCount)

	prevIdx := make(map[int]struct{})
	for i := range delHashes {
		var idx int
		for {
			if addCount == 1 {
				idx = 0
				prevIdx[idx] = struct{}{}
				break
			} else {
				idx = rand.Intn(int(addCount))
				_, found := prevIdx[idx]
				if !found {
					prevIdx[idx] = struct{}{}
					break
				}
			}
		}

		delHashes[i] = leaves[idx].Hash
		delTargets[i] = uint64(idx)
	}

	return leaves, delHashes, delTargets
}

// proofSanity checks that a given proof doesn't have duplicate targets.
func proofSanity(proof Proof) error {
	targetMap := make(map[uint64]int)
	for idx, target := range proof.Targets {
		foundIdx, found := targetMap[target]
		if found {
			return fmt.Errorf("proofSanity fail. Found duplicate target "+
				"at idx %d and %d in targets: %v", foundIdx, idx, proof.Targets)
		}

		targetMap[target] = idx
	}

	return nil
}

func FuzzModify(f *testing.F) {
	var tests = []struct {
		startLeaves uint32
		modifyAdds  uint32
		delCount    uint32
	}{
		{
			8,
			2,
			3,
		},
		{
			6,
			2,
			5,
		},
	}
	for _, test := range tests {
		f.Add(test.startLeaves, test.modifyAdds, test.delCount)
	}

	f.Fuzz(func(t *testing.T, startLeaves uint32, modifyAdds uint32, delCount uint32) {
		// delCount must be less than the current number of leaves.
		if delCount > startLeaves {
			return
		}

		p := NewAccumulator(true)
		leaves, delHashes, delTargets := getAddsAndDels(uint32(p.numLeaves), startLeaves, delCount)
		err := p.Modify(leaves, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		beforeStr := p.String()
		beforeMap := nodeMapToString(p.nodeMap)

		modifyLeaves, _, _ := getAddsAndDels(uint32(p.numLeaves), modifyAdds, 0)
		err = p.Modify(modifyLeaves, delHashes, delTargets)
		if err != nil {
			t.Fatal(err)
		}
		afterStr := p.String()
		afterMap := nodeMapToString(p.nodeMap)

		err = p.checkHashes()
		if err != nil {
			t.Fatal(err)
		}

		if uint64(len(p.nodeMap)) != p.numLeaves-p.numDels {
			startHashes := make([]Hash, len(leaves))
			for i, leaf := range leaves {
				startHashes[i] = leaf.Hash
			}

			modifyHashes := make([]Hash, len(modifyLeaves))
			for i, leaf := range modifyLeaves {
				modifyHashes[i] = leaf.Hash
			}
			err := fmt.Errorf("FuzzModify fail: have %d leaves in map but %d leaves in total. "+
				"\nbefore:\n\n%s"+
				"\nafter:\n\n%s"+
				"\nstartLeaves %d, modifyAdds %d, delCount %d, "+
				"\nstartHashes:\n%s"+
				"\nmodifyAdds:\n%s"+
				"\nmodifyDels:\n%s"+
				"\ndel targets:\n %v"+
				"\nnodemap before modify:\n %s"+
				"\nnodemap after modify:\n %s",
				len(p.nodeMap), p.numLeaves-p.numDels,
				beforeStr,
				afterStr,
				startLeaves, modifyAdds, delCount,
				printHashes(startHashes),
				printHashes(modifyHashes),
				printHashes(delHashes),
				delTargets,
				beforeMap,
				afterMap)
			t.Fatal(err)
		}

		err = p.posMapSanity()
		if err != nil {
			t.Fatal(err)
		}
	})
}

func FuzzModifyChain(f *testing.F) {
	var tests = []struct {
		numAdds  uint32
		duration uint32
		seed     int64
	}{
		{3, 0x07, 0x07},
	}
	for _, test := range tests {
		f.Add(test.numAdds, test.duration, test.seed)
	}

	f.Fuzz(func(t *testing.T, numAdds, duration uint32, seed int64) {
		// simulate blocks with simchain
		sc := newSimChainWithSeed(duration, seed)

		p := NewAccumulator(true)
		var totalAdds, totalDels int
		for b := 0; b <= 100; b++ {
			adds, _, delHashes := sc.NextBlock(numAdds)
			totalAdds += len(adds)
			totalDels += len(delHashes)

			proof, err := p.Prove(delHashes)
			if err != nil {
				t.Fatalf("FuzzModifyChain fail at block %d. Error: %v", b, err)
			}

			err = p.Verify(delHashes, proof)
			if err != nil {
				t.Fatal(err)
			}

			for _, target := range proof.Targets {
				n, _, _, err := p.getNode(target)
				if err != nil {
					t.Fatalf("FuzzModifyChain fail at block %d. Error: %v", b, err)
				}
				if n == nil {
					t.Fatalf("FuzzModifyChain fail to read %d at block %d.", target, b)
				}
			}

			err = p.Modify(adds, delHashes, proof.Targets)
			if err != nil {
				t.Fatalf("FuzzModifyChain fail at block %d. Error: %v", b, err)
			}

			if b%10 == 0 {
				err = p.checkHashes()
				if err != nil {
					t.Fatal(err)
				}
			}

			err = p.posMapSanity()
			if err != nil {
				t.Fatalf("FuzzModifyChain fail at block %d. Error: %v",
					b, err)
			}
			if uint64(len(p.nodeMap)) != p.numLeaves-p.numDels {
				err := fmt.Errorf("FuzzModifyChain fail at block %d: "+
					"have %d leaves in map but only %d leaves in total",
					b, len(p.nodeMap), p.numLeaves-p.numDels)
				t.Fatal(err)
			}

			err = p.positionSanity()
			if err != nil {
				t.Fatal(err)
			}
		}
	})
}
