package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/byzcoin"
	"github.com/dedis/cothority/byzcoin/bcadmin/lib"
	"github.com/dedis/cothority/byzcoin/contracts"
	"github.com/dedis/cothority/darc"
	"github.com/dedis/cothority/darc/expression"
	"github.com/dedis/kyber"
	"github.com/dedis/kyber/util/encoding"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/onet"
	"github.com/dedis/onet/cfgpath"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/dedis/protobuf"

	cli "gopkg.in/urfave/cli.v1"
)

func init() {
	network.RegisterMessages(&darc.Darc{}, &darc.Identity{}, &darc.Signer{})
}

var cmds = cli.Commands{
	{
		Name:      "join",
		Usage:     "joins a given byzcoin instance",
		ArgsUsage: "bc-xxx.cfg",
		Action:    join,
	},
	{
		Name:    "show",
		Usage:   "shows the account address and the balance",
		Aliases: []string{"s"},
		Action:  show,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "address",
				Usage: "show coin address (InstanceID)",
			},
		},
	},
	{
		Name:      "transfer",
		Usage:     "transfer coins from your account to another one",
		ArgsUsage: "public key of account",
		Action:    transfer,
		Flags: []cli.Flag{
			cli.IntFlag{
				Name:  "multi",
				Usage: "to send multiple transactions and measure tps",
				Value: 1,
			}},
	},
}

type Config struct {
	BCConfig lib.Config
	KeyPair  key.Pair
}

var cliApp = cli.NewApp()
var ConfigPath string

// getDataPath is a function pointer so that tests can hook and modify this.
var getDataPath = cfgpath.GetDataPath

func init() {
	cliApp.Name = "wallet"
	cliApp.Usage = "Handle wallet data"
	cliApp.Version = "0.1"
	cliApp.Commands = cmds
	cliApp.Flags = []cli.Flag{
		cli.IntFlag{
			Name:  "debug, d",
			Value: 0,
			Usage: "debug-level: 1 for terse, 5 for maximal",
		},
		cli.StringFlag{
			Name:  "config, c",
			Value: getDataPath(cliApp.Name),
			Usage: "path to configuration-directory",
		},
	}
	cliApp.Before = func(c *cli.Context) error {
		log.SetDebugVisible(c.Int("debug"))
		ConfigPath = c.String("config")
		return nil
	}
}

func main() {
	log.ErrFatal(cliApp.Run(os.Args))
}

func join(c *cli.Context) error {
	if _, _, err := loadConfig(); err == nil {
		return fmt.Errorf("configuration already exists - please delete %s first",
			filepath.Join(ConfigPath, configName))
	}
	if c.NArg() < 1 {
		return errors.New("please give bc-xxx.cfg")
	}

	bcCfg, _, err := lib.LoadConfig(c.Args().First())
	if err != nil {
		return err
	}

	cfg := Config{
		BCConfig: bcCfg,
		KeyPair:  *key.NewKeyPair(cothority.Suite),
	}

	err = cfg.save()
	if err != nil {
		return err
	}

	return show(c)
}

func show(c *cli.Context) error {
	cfg, cl, err := loadConfig()
	if err != nil {
		return err
	}

	iid, err := coinHashPub(cfg.KeyPair.Public)
	if err != nil {
		return err
	}
	resp, err := cl.GetProof(iid.Slice())
	if err != nil {
		return err
	}
	var balance uint64
	if resp.Proof.InclusionProof.Match(iid.Slice()) {
		_, value, _, _, err := resp.Proof.KeyValue()
		if err != nil {
			return err
		}
		var coin byzcoin.Coin
		err = protobuf.Decode(value, &coin)
		if err != nil {
			return err
		}
		balance = coin.Value
	}
	log.Info("Public key is:", cfg.KeyPair.Public)
	if c.Bool("address") {
		log.Info("Coin-address is:", iid)
	}
	log.Info("Balance is:", balance)
	return nil
}

func transfer(c *cli.Context) error {
	if c.NArg() < 2 {
		return errors.New("please give the following arguments: balance address")
	}
	amount, err := strconv.ParseUint(c.Args().First(), 10, 64)
	if err != nil {
		return err
	}

	targetBuf, err := hex.DecodeString(c.Args().Get(1))
	if err != nil {
		return err
	}
	target, err := coinHash(targetBuf)

	cfg, cl, err := loadConfig()
	if err != nil {
		return err
	}

	iid, err := coinHashPub(cfg.KeyPair.Public)
	if err != nil {
		return err
	}
	resp, err := cl.GetProof(iid.Slice())
	if err != nil {
		return err
	}
	var balance uint64
	if resp.Proof.InclusionProof.Match(iid.Slice()) {
		_, value, _, _, err := resp.Proof.KeyValue()
		if err != nil {
			return err
		}
		var coin byzcoin.Coin
		err = protobuf.Decode(value, &coin)
		if err != nil {
			return err
		}
		balance = coin.Value
	}
	if amount > balance {
		return errors.New("your account doesn't have enough coins in it")
	}

	signer := darc.NewSignerEd25519(cfg.KeyPair.Public, cfg.KeyPair.Private)
	counters, err := cl.GetSignerCounters(signer.Identity().String())
	multi := c.Int("multi")
	if multi > 200 {
		log.Warn("Only allowing 200 transactions at a time")
		multi = 200
	}
	for tx := 0; tx < multi; tx++ {
		counters.Counters[0]++
		amountBuf := make([]byte, 8)
		binary.LittleEndian.PutUint64(amountBuf, amount)
		ctx := byzcoin.ClientTransaction{
			Instructions: byzcoin.Instructions{
				{
					InstanceID: iid,
					Invoke: &byzcoin.Invoke{
						Command: "transfer",
						Args: byzcoin.Arguments{
							{
								Name:  "coins",
								Value: amountBuf,
							},
							{
								Name:  "destination",
								Value: target.Slice(),
							},
						},
					},
					SignerCounter: counters.Counters,
				},
			},
		}
		ctx.SignWith(signer)

		log.Info("Sending transaction of", amount, "coins to address", c.Args().Get(1))
		wait := 0
		if tx == multi-1 {
			wait = 10
		}
		_, err = cl.AddTransactionAndWait(ctx, wait)
		if err != nil {
			return err
		}
	}

	log.Info("Transaction succeeded")

	return nil
}

func coinHashPub(pub kyber.Point) (iid byzcoin.InstanceID, err error) {
	buf, err := pub.MarshalBinary()
	if err != nil {
		return
	}
	return coinHash(buf)
}

func coinHash(buf []byte) (iid byzcoin.InstanceID, err error) {
	h := sha256.New()
	h.Write([]byte(contracts.ContractCoinID))
	h.Write(buf)
	iid = byzcoin.NewInstanceID(h.Sum(nil))
	return
}

const configName = "wallet.json"

// All these structures are used to save/load json files. This is due to the
// fact that points and scalars are not storable in json. An alternative would
// be to add `TextMarshaller` to Point, Scalar and the IDs.

type siJSON struct {
	Public      string
	ID          string
	Address     string
	Description string
}

type rosterJSON struct {
	ID        string
	List      []siJSON
	Aggregate string
}

type ruleJSON struct {
	Action     string
	Expression string
}

type darcJSON struct {
	Version     uint64
	Description string
	BaseID      string
	PrevID      string
	Rules       []ruleJSON
}

type bcconfigJSON struct {
	Roster        rosterJSON
	ByzCoinID     string
	GenesisDarc   darcJSON
	AdminIdentity string
}

type keyPairJSON struct {
	Public  string
	Private string
}

type configJSON struct {
	ByzcoinConfig bcconfigJSON
	KeyPair       keyPairJSON
}

func loadConfig() (cfg Config, cl *byzcoin.Client, err error) {
	buf, err := ioutil.ReadFile(filepath.Join(ConfigPath, configName))
	if err != nil {
		return
	}
	cfgJSON := configJSON{}
	err = json.Unmarshal(buf, &cfgJSON)
	if err != nil {
		return
	}
	pub, err := encoding.StringHexToPoint(cothority.Suite, cfgJSON.KeyPair.Public)
	if err != nil {
		return
	}
	priv, err := encoding.StringHexToScalar(cothority.Suite, cfgJSON.KeyPair.Private)
	if err != nil {
		return
	}
	cfg.KeyPair = key.Pair{
		Public:  pub,
		Private: priv,
	}

	var list []*network.ServerIdentity
	for _, siJ := range cfgJSON.ByzcoinConfig.Roster.List {
		pub, err = encoding.StringHexToPoint(cothority.Suite, siJ.Public)
		if err != nil {
			return
		}
		si := network.NewServerIdentity(pub, network.Address(siJ.Address))
		si.Description = siJ.Description
		var id []byte
		id, err = hex.DecodeString(siJ.ID)
		if err != nil {
			return
		}
		copy(si.ID[:], id)
		list = append(list, si)
	}
	cfg.BCConfig.Roster = *onet.NewRoster(list)
	cfg.BCConfig.ByzCoinID, err = hex.DecodeString(cfgJSON.ByzcoinConfig.ByzCoinID)
	if err != nil {
		return
	}

	dj := cfgJSON.ByzcoinConfig.GenesisDarc
	cfg.BCConfig.GenesisDarc.Version = dj.Version
	cfg.BCConfig.GenesisDarc.Description = []byte(dj.Description)
	cfg.BCConfig.GenesisDarc.BaseID, err = hex.DecodeString(dj.BaseID)
	if err != nil {
		return
	}
	cfg.BCConfig.GenesisDarc.PrevID, err = hex.DecodeString(dj.PrevID)
	if err != nil {
		return
	}
	for _, rul := range dj.Rules {
		cfg.BCConfig.GenesisDarc.Rules.List = append(cfg.BCConfig.GenesisDarc.Rules.List, darc.Rule{darc.Action(rul.Action), expression.Expr(rul.Expression)})
	}

	adminPub, err := encoding.StringHexToPoint(cothority.Suite, cfgJSON.ByzcoinConfig.AdminIdentity)
	cfg.BCConfig.AdminIdentity.Ed25519 = &darc.IdentityEd25519{adminPub}

	cl = byzcoin.NewClient(cfg.BCConfig.ByzCoinID, cfg.BCConfig.Roster)
	return
}

func (cfg Config) save() error {
	kpPub, err := encoding.PointToStringHex(cothority.Suite, cfg.KeyPair.Public)
	if err != nil {
		return err
	}
	kpPriv, err := encoding.ScalarToStringHex(cothority.Suite, cfg.KeyPair.Private)
	if err != nil {
		return err
	}

	jr := rosterJSON{
		ID:        fmt.Sprintf("%x", cfg.BCConfig.Roster.ID[:]),
		Aggregate: cfg.BCConfig.Roster.Aggregate.String(),
	}
	for _, si := range cfg.BCConfig.Roster.List {
		jr.List = append(jr.List, siJSON{
			Public:      si.Public.String(),
			ID:          fmt.Sprintf("%x", si.ID[:]),
			Address:     string(si.Address),
			Description: si.Description,
		})
	}
	d := cfg.BCConfig.GenesisDarc
	jd := darcJSON{
		Version:     d.Version,
		Description: string(d.Description),
		BaseID:      fmt.Sprintf("%x", d.BaseID),
		PrevID:      fmt.Sprintf("%x", d.PrevID),
	}
	for _, r := range d.Rules.List {
		jd.Rules = append(jd.Rules, ruleJSON{
			Action:     string(r.Action),
			Expression: string(r.Expr),
		})
	}
	cfgJSON := configJSON{
		KeyPair: keyPairJSON{kpPub, kpPriv},
		ByzcoinConfig: bcconfigJSON{
			Roster:        jr,
			ByzCoinID:     fmt.Sprintf("%x", cfg.BCConfig.ByzCoinID),
			GenesisDarc:   jd,
			AdminIdentity: cfg.BCConfig.AdminIdentity.Ed25519.Point.String(),
		},
	}

	buf, err := json.MarshalIndent(cfgJSON, "", " ")

	os.MkdirAll(ConfigPath, 0700)
	return ioutil.WriteFile(filepath.Join(ConfigPath, configName), buf, 0600)
}
