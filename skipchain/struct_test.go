package skipchain

import (
	"bytes"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.dedis.ch/kyber/v3/suites"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/log"
	"go.dedis.ch/onet/v3/network"
	bbolt "go.etcd.io/bbolt"
)

func TestSkipBlock_GetResponsible(t *testing.T) {
	l := onet.NewTCPTest(suite)
	_, roster, _ := l.GenTree(3, true)
	defer l.CloseAll()

	db, fname := setupSkipBlockDB(t)
	defer db.Close()
	defer os.Remove(fname)

	root0 := NewSkipBlock()
	root0.Roster = roster
	root0.Hash = root0.CalculateHash()
	root0.BackLinkIDs = []SkipBlockID{root0.Hash}
	db.Store(root0)
	root1 := root0.Copy()
	root1.Index++
	db.Store(root1)
	inter0 := NewSkipBlock()
	inter0.Roster = roster
	inter0.Hash = inter0.CalculateHash()
	db.Store(inter0)
	inter1 := inter0.Copy()
	inter1.Index++
	inter1.BackLinkIDs = []SkipBlockID{inter0.Hash}

	b, err := db.GetResponsible(root0)
	log.ErrFatal(err)
	assert.True(t, root0.Equal(b))

	b, err = db.GetResponsible(root1)
	log.ErrFatal(err)
	assert.True(t, root0.Equal(b))

	b, err = db.GetResponsible(inter0)
	log.ErrFatal(err)
	assert.Equal(t, root1.Hash, b.Hash)

	b, err = db.GetResponsible(inter1)
	log.ErrFatal(err)
	assert.True(t, inter0.Equal(b))
}

func TestSkipBlock_VerifySignatures(t *testing.T) {
	l := onet.NewTCPTest(suite)
	_, roster3, _ := l.GenTree(3, true)
	defer l.CloseAll()
	roster2 := onet.NewRoster(roster3.List[0:2])

	db, fname := setupSkipBlockDB(t)
	defer db.Close()
	defer os.Remove(fname)

	root := NewSkipBlock()
	root.Roster = roster2
	root.BackLinkIDs = append(root.BackLinkIDs, SkipBlockID{1, 2, 3, 4})
	root.Hash = root.CalculateHash()
	db.Store(root)
	log.ErrFatal(root.VerifyForwardSignatures())
	log.ErrFatal(db.VerifyLinks(root))

	block1 := root.Copy()
	block1.BackLinkIDs = append(block1.BackLinkIDs, root.Hash)
	block1.Index++
	db.Store(block1)
	require.Nil(t, block1.VerifyForwardSignatures())
	require.NotNil(t, db.VerifyLinks(block1))
}

func TestSkipBlock_Hash1(t *testing.T) {
	// Needed for the roster.
	s := suites.MustFind("ed25519")
	si := network.NewServerIdentity(s.Point(), "tcp://127.0.0.1:2000")

	sbd1 := NewSkipBlock()
	sbd1.Data = []byte("1")
	sbd1.Height = 4
	sbd1.Roster = onet.NewRoster([]*network.ServerIdentity{si})
	h1 := sbd1.updateHash()
	assert.Equal(t, h1, sbd1.Hash)
	// Dump the current hash, to put it into the Java test.
	//t.Logf("%x", h1) // 1304bd5ecad8d54a2fd7b81a8864f698966308104b20780b634c4b237b843823

	// Dumping the skipblock, to put it into the Java test.
	// buf, err := protobuf.Encode(sbd1)
	// require.NoError(t, err)
	// t.Logf("%x", buf) // 08001008180020003a004201314a94010a106bc1027de8ef542e8b09219c287b2fde12560a2865642e706f696e7400000000000000000000000000000000000000000000000000000000000000001a103809e37975a45b4a865899668d645d9522147463703a2f2f3132372e302e302e313a323030302a003a001a2865642e706f696e74000000000000000000000000000000000000000000000000000000000000000052201304bd5ecad8d54a2fd7b81a8864f698966308104b20780b634c4b237b8438236200

	// Clone: equal
	sbd2 := sbd1.Copy()
	assert.Equal(t, sbd2.Hash, sbd1.Hash)
	// update with no changes: still equal
	h2 := sbd2.updateHash()
	assert.Equal(t, h1, h2)

	// Change height: not equal
	sbd2.Height++
	h2 = sbd2.updateHash()
	assert.NotEqual(t, h1, h2)

	// Clone, then change field Data: not equal
	sbd2 = sbd1.Copy()
	sbd2.Data[0]++
	h2 = sbd2.updateHash()
	assert.NotEqual(t, h1, h2)
}

func TestSkipBlock_Hash2(t *testing.T) {
	local := onet.NewLocalTest(suite)
	hosts, el, _ := local.GenTree(2, false)
	defer local.CloseAll()
	sbd1 := NewSkipBlock()
	sbd1.Roster = el
	sbd1.Height = 1
	h1 := sbd1.updateHash()
	assert.Equal(t, h1, sbd1.Hash)

	sbd2 := NewSkipBlock()
	sbd2.Roster = local.GenRosterFromHost(hosts[0])
	sbd2.Height = 1
	h2 := sbd2.updateHash()
	assert.NotEqual(t, h1, h2)
}

func TestBlockLink_Copy(t *testing.T) {
	// Test if copy is deep or only shallow
	b1 := &ForwardLink{}
	b1.Signature.Sig = []byte{1}
	b2 := b1.Copy()
	b2.Signature.Sig[0] = byte(2)
	if bytes.Equal(b1.Signature.Sig, b2.Signature.Sig) {
		t.Fatal("They should not be equal")
	}

	sb1 := NewSkipBlock()
	sb2 := sb1.Copy()
	sb1.Height = 10
	sb2.Height = 20
	if sb1.Height == sb2.Height {
		t.Fatal("Should not be equal")
	}
}

func TestSkipBlock_GetFuzzy(t *testing.T) {
	db, fname := setupSkipBlockDB(t)
	defer db.Close()
	defer os.Remove(fname)

	sb0 := NewSkipBlock()
	sb0.Data = []byte{0}
	sb0.Hash = []byte{1, 2, 3, 6, 5}

	sb1 := NewSkipBlock()
	sb1.Data = []byte{1}
	sb1.Hash = []byte{2, 3, 4, 1, 5}

	db.Update(func(tx *bbolt.Tx) error {
		err := db.storeToTx(tx, sb0)
		require.Nil(t, err)

		err = db.storeToTx(tx, sb1)
		require.Nil(t, err)
		return nil
	})

	sb, err := db.GetFuzzy("")
	require.Nil(t, sb)
	require.NotNil(t, err)

	sb, err = db.GetFuzzy("01")
	require.Nil(t, err)
	require.NotNil(t, sb)
	require.Equal(t, sb.Data[0], sb0.Data[0])

	sb, err = db.GetFuzzy("02")
	require.Nil(t, err)
	require.NotNil(t, sb)
	require.Equal(t, sb.Data[0], sb1.Data[0])

	sb, err = db.GetFuzzy("03")
	require.Nil(t, err)
	require.Nil(t, sb)

	sb, err = db.GetFuzzy("04")
	require.Nil(t, err)
	require.Nil(t, sb)

	sb, err = db.GetFuzzy("05")
	require.Nil(t, err)
	require.NotNil(t, sb)
	require.Equal(t, sb.Data[0], sb0.Data[0])

	sb, err = db.GetFuzzy("06")
	require.Nil(t, err)
	require.Nil(t, sb)

	sb, err = db.GetFuzzy("0102030605")
	require.Nil(t, err)
	require.NotNil(t, sb)
	require.Equal(t, sb.Data[0], sb0.Data[0])
}

func TestSkipBlock_Payload(t *testing.T) {
	sb := NewSkipBlock()
	h := sb.CalculateHash()
	sb.Payload = []byte{1, 2, 3}
	require.Equal(t, h, sb.CalculateHash())
}

// This checks if the it returns the shortest path or an error
// when blocks are missing
func TestGetProof(t *testing.T) {
	db, file := setupSkipBlockDB(t)
	defer os.Remove(file)

	root := NewSkipBlock()
	root.updateHash()
	sb1 := NewSkipBlock()
	sb1.BackLinkIDs = []SkipBlockID{root.Hash}
	sb1.updateHash()
	sb2 := NewSkipBlock()
	sb2.BackLinkIDs = []SkipBlockID{sb1.Hash}
	sb2.updateHash()
	sb1.ForwardLink = []*ForwardLink{&ForwardLink{To: sb2.Hash}}
	root.ForwardLink = []*ForwardLink{&ForwardLink{To: sb1.Hash}, &ForwardLink{To: sb2.Hash}}

	_, err := db.StoreBlocks([]*SkipBlock{root, sb1, sb2})
	require.Nil(t, err)

	blocks, err := db.GetProof(root.Hash)
	require.Nil(t, err)
	require.Equal(t, 2, len(blocks))
	require.True(t, blocks[1].Hash.Equal(sb2.Hash))

	err = db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(db.bucketName).Delete(sb2.Hash)
	})
	require.Nil(t, err)

	_, err = db.GetProof(root.Hash)
	require.NotNil(t, err)
}

// setupSkipBlockDB initialises a database with a bucket called 'skipblock-test' inside.
// The caller is responsible to close and remove the database file after using it.
func setupSkipBlockDB(t *testing.T) (*SkipBlockDB, string) {
	f, err := ioutil.TempFile("", "skipblock-test")
	require.Nil(t, err)
	fname := f.Name()
	require.Nil(t, f.Close())

	db, err := bbolt.Open(fname, 0600, nil)
	require.Nil(t, err)

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucket([]byte("skipblock-test"))
		return err
	})
	require.Nil(t, err)

	return NewSkipBlockDB(db, []byte("skipblock-test")), fname
}

// Checks if the buffer api works as expected
func TestBlockBuffer(t *testing.T) {
	bb := newSkipBlockBuffer()
	sid := []byte{1}
	bid := []byte{2}

	sb := NewSkipBlock()
	sb.Index = 1
	sb.GenesisID = sid
	sb.Hash = bid
	bb.add(sb)

	sb = bb.get(sid, bid)
	require.NotNil(t, sb)

	// wrong key
	sb = bb.get(bid, bid)
	require.Nil(t, sb)

	// wrong block id
	sb = bb.get(sid, sid)
	require.Nil(t, sb)

	bb.clear(sid)
	sb = bb.get(sid, bid)
	require.Nil(t, sb)
}
