// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"errors"
	"fmt"

	stdmath "math"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/snow"
	"github.com/ava-labs/gecko/utils/codec"
	"github.com/ava-labs/gecko/utils/crypto"
	"github.com/ava-labs/gecko/utils/logging"
	safemath "github.com/ava-labs/gecko/utils/math"
	"github.com/ava-labs/gecko/utils/timer"
	"github.com/ava-labs/gecko/utils/wrappers"
	"github.com/ava-labs/gecko/vms/components/avax"
	"github.com/ava-labs/gecko/vms/secp256k1fx"
)

// Wallet is a holder for keys and UTXOs for the Avalanche DAG.
type Wallet struct {
	networkID   uint32
	chainID     ids.ID
	avaxAssetID ids.ID

	clock timer.Clock
	codec codec.Codec
	log   logging.Logger

	keychain *secp256k1fx.Keychain // Mapping from public address to the SigningKeys
	utxoSet  *UTXOSet              // Mapping from utxoIDs to UTXOs

	balance map[[32]byte]uint64
	txFee   uint64

	txs []*Tx
}

// NewWallet returns a new Wallet
func NewWallet(log logging.Logger, networkID uint32, chainID, avaxAssetID ids.ID, txFee uint64) (*Wallet, error) {
	c := codec.NewDefault()
	errs := wrappers.Errs{}
	errs.Add(
		c.RegisterType(&BaseTx{}),
		c.RegisterType(&CreateAssetTx{}),
		c.RegisterType(&OperationTx{}),
		c.RegisterType(&ImportTx{}),
		c.RegisterType(&ExportTx{}),
		c.RegisterType(&secp256k1fx.TransferInput{}),
		c.RegisterType(&secp256k1fx.MintOutput{}),
		c.RegisterType(&secp256k1fx.TransferOutput{}),
		c.RegisterType(&secp256k1fx.MintOperation{}),
		c.RegisterType(&secp256k1fx.Credential{}),
	)
	return &Wallet{
		networkID:   networkID,
		chainID:     chainID,
		avaxAssetID: avaxAssetID,
		codec:       c,
		log:         log,
		keychain:    secp256k1fx.NewKeychain(),
		utxoSet:     &UTXOSet{},
		balance:     make(map[[32]byte]uint64),
		txFee:       txFee,
	}, errs.Err
}

// Codec returns the codec used for serialization
func (w *Wallet) Codec() codec.Codec { return w.codec }

// GetAddress returns one of the addresses this wallet manages. If no address
// exists, one will be created.
func (w *Wallet) GetAddress() (ids.ShortID, error) {
	if w.keychain.Addrs.Len() == 0 {
		return w.CreateAddress()
	}
	return w.keychain.Addrs.CappedList(1)[0], nil
}

// CreateAddress returns a new address.
// It also saves the address and the private key that controls it
// so the address can be used later
func (w *Wallet) CreateAddress() (ids.ShortID, error) {
	privKey, err := w.keychain.New()
	return privKey.PublicKey().Address(), err
}

// ImportKey imports a private key into this wallet
func (w *Wallet) ImportKey(sk *crypto.PrivateKeySECP256K1R) { w.keychain.Add(sk) }

// AddUTXO adds a new UTXO to this wallet if this wallet may spend it
// The UTXO's output must be an OutputPayment
func (w *Wallet) AddUTXO(utxo *avax.UTXO) {
	out, ok := utxo.Out.(avax.TransferableOut)
	if !ok {
		return
	}

	if _, _, err := w.keychain.Spend(out, stdmath.MaxUint64); err == nil {
		w.utxoSet.Put(utxo)
		w.balance[utxo.AssetID().Key()] += out.Amount()
	}
}

// RemoveUTXO from this wallet
func (w *Wallet) RemoveUTXO(utxoID ids.ID) {
	utxo := w.utxoSet.Get(utxoID)
	if utxo == nil {
		return
	}

	assetID := utxo.AssetID()
	assetKey := assetID.Key()
	newBalance := w.balance[assetKey] - utxo.Out.(avax.TransferableOut).Amount()
	if newBalance == 0 {
		delete(w.balance, assetKey)
	} else {
		w.balance[assetKey] = newBalance
	}

	w.utxoSet.Remove(utxoID)
}

// Balance returns the amount of the assets in this wallet
func (w *Wallet) Balance(assetID ids.ID) uint64 { return w.balance[assetID.Key()] }

// CreateTx returns a tx that sends [amount] of [assetID] to [destAddr]
func (w *Wallet) CreateTx(assetID ids.ID, amount uint64, destAddr ids.ShortID) (*Tx, error) {
	if amount == 0 {
		return nil, errors.New("invalid amount")
	}

	amounts := map[[32]byte]uint64{
		assetID.Key(): uint64(amount),
	}

	amountsWithFee := make(map[[32]byte]uint64, len(amounts)+1)
	for k, v := range amounts {
		amountsWithFee[k] = v
	}
	avaxKey := w.avaxAssetID.Key()
	amountWithFee, err := safemath.Add64(amountsWithFee[avaxKey], w.txFee)
	if err != nil {
		return nil, fmt.Errorf("problem calculating required spend amount: %w", err)
	}
	amountsWithFee[avaxKey] = amountWithFee

	amountsSpent := make(map[[32]byte]uint64, len(amounts))
	time := w.clock.Unix()

	ins := []*avax.TransferableInput{}
	keys := [][]*crypto.PrivateKeySECP256K1R{}
	for _, utxo := range w.utxoSet.UTXOs {
		assetID := utxo.AssetID()
		assetKey := assetID.Key()
		amount := amountsWithFee[assetKey]
		amountSpent := amountsSpent[assetKey]

		if amountSpent >= amount {
			// we already have enough inputs allocated to this asset
			continue
		}

		inputIntf, signers, err := w.keychain.Spend(utxo.Out, time)
		if err != nil {
			// this utxo can't be spent with the current keys right now
			continue
		}
		input, ok := inputIntf.(avax.TransferableIn)
		if !ok {
			// this input doesn't have an amount, so I don't care about it here
			continue
		}
		newAmountSpent, err := safemath.Add64(amountSpent, input.Amount())
		if err != nil {
			// there was an error calculating the consumed amount, just error
			return nil, errSpendOverflow
		}
		amountsSpent[assetKey] = newAmountSpent

		// add the new input to the array
		ins = append(ins, &avax.TransferableInput{
			UTXOID: utxo.UTXOID,
			Asset:  avax.Asset{ID: assetID},
			In:     input,
		})
		// add the required keys to the array
		keys = append(keys, signers)
	}

	// Check if the amounts spent covers the amount plus the fee
	for asset, amount := range amountsWithFee {
		if amountsSpent[asset] < amount {
			return nil, errInsufficientFunds
		}
	}

	avax.SortTransferableInputsWithSigners(ins, keys)

	outs := []*avax.TransferableOutput{}
	for asset, amountWithFee := range amountsWithFee {
		assetID := ids.NewID(asset)
		amount := amounts[asset]
		amountSpent := amountsSpent[asset]

		if amount > 0 {
			outs = append(outs, &avax.TransferableOutput{
				Asset: avax.Asset{ID: assetID},
				Out: &secp256k1fx.TransferOutput{
					Amt: amount,
					OutputOwners: secp256k1fx.OutputOwners{
						Locktime:  0,
						Threshold: 1,
						Addrs:     []ids.ShortID{destAddr},
					},
				},
			})
		}

		if amountSpent > amountWithFee {
			changeAddr := w.keychain.Keys[0].PublicKey().Address()
			outs = append(outs, &avax.TransferableOutput{
				Asset: avax.Asset{ID: assetID},
				Out: &secp256k1fx.TransferOutput{
					Amt: amountSpent - amountWithFee,
					OutputOwners: secp256k1fx.OutputOwners{
						Locktime:  0,
						Threshold: 1,
						Addrs:     []ids.ShortID{changeAddr},
					},
				},
			})
		}
	}

	avax.SortTransferableOutputs(outs, w.codec)

	tx := &Tx{UnsignedTx: &BaseTx{BaseTx: avax.BaseTx{
		NetworkID:    w.networkID,
		BlockchainID: w.chainID,
		Outs:         outs,
		Ins:          ins,
	}}}
	return tx, tx.SignSECP256K1Fx(w.codec, keys)
}

// GenerateTxs generates the transactions that will be sent
// during the test
// Generate them all on test initialization so tx generation is not bottleneck
// in testing
func (w *Wallet) GenerateTxs(numTxs int, assetID ids.ID) error {
	w.log.Info("Generating %d transactions", numTxs)

	ctx := snow.DefaultContextTest()
	ctx.NetworkID = w.networkID
	ctx.ChainID = w.chainID

	frequency := numTxs / 50
	if frequency > 1000 {
		frequency = 1000
	}
	if frequency == 0 {
		frequency = 1
	}

	w.txs = make([]*Tx, numTxs)
	for i := 0; i < numTxs; i++ {
		addr, err := w.CreateAddress()
		if err != nil {
			return err
		}
		tx, err := w.CreateTx(assetID, 1, addr)
		if err != nil {
			return err
		}

		for _, utxoID := range tx.InputUTXOs() {
			w.RemoveUTXO(utxoID.InputID())
		}
		for _, utxo := range tx.UTXOs() {
			w.AddUTXO(utxo)
		}

		if numGenerated := i + 1; numGenerated%frequency == 0 {
			w.log.Info("Generated %d out of %d transactions", numGenerated, numTxs)
		}

		w.txs[i] = tx
	}

	w.log.Info("Finished generating %d transactions", numTxs)
	return nil
}

// NextTx returns the next tx to be sent as part of xput test
func (w *Wallet) NextTx() (*Tx, error) {
	if len(w.txs) == 0 {
		return nil, errors.New("no more transactions remaining")
	}
	tx := w.txs[0]
	w.txs = w.txs[1:]
	return tx, nil
}

func (w *Wallet) String() string {
	return fmt.Sprintf(
		"Keychain:\n"+
			"%s\n"+
			"%s",
		w.keychain.PrefixedString("    "),
		w.utxoSet.PrefixedString("    "),
	)
}
