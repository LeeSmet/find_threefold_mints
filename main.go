package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/threefoldfoundation/tfchain/cmd/tfchaint/explorer"
	tfcli "github.com/threefoldfoundation/tfchain/extensions/tfchain/client"
	"github.com/threefoldtech/rivine/pkg/api"
	"github.com/threefoldtech/rivine/pkg/client"
	"github.com/threefoldtech/rivine/types"
)

const tftasset = "TFT:GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47"
const tftaasset = "TFTA:GBUT4GP5GJ6B3XW5PXENHQA7TXJI5GOPW3NF4W3ZIW6OOO4ISY6WNLN2"
const tftIssuer = "GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47"
const tftaIssuer = "GBUT4GP5GJ6B3XW5PXENHQA7TXJI5GOPW3NF4W3ZIW6OOO4ISY6WNLN2"

const TransactionVersionMinterDefinition types.TransactionVersion = 129

type rivineMint struct {
	txID   string
	ts     time.Time
	to     string
	amount uint64
	memo   string
}

type mint struct {
	txID   string
	ts     time.Time
	to     string
	amount uint64
	memo   string
}

type burn struct {
	txID   string
	ts     time.Time
	from   string
	amount uint64
}

// Set up the transaction controllers, needed to decode mint txes (and others we
// don't care about, but which would cause failure of validation).
func init() {
	explorer := explorer.NewExplorer("https://explorer2.threefoldtoken.com", "Rivine-Agent", "")
	bc, err := client.NewBaseClient(explorer, nil)
	if err != nil {
		panic(err)
	}
	tfcli.RegisterStandardTransactions(bc)
}


func main() {
	rivMints, err := getRivineMints()
	if err != nil {
		panic(err)
	}

	fmt.Println("============")
	fmt.Println("rivine mints")
	fmt.Println("============")

	for _, rmint := range rivMints {
		fmt.Printf("%s,%s,%s,%s,%s\n", rmint.txID, rmint.ts.Format(time.RFC822), rmint.to, rivineToString(rmint.amount), rmint.memo)
	}

	tftaMints, tftaBurns, err := findAccPayments(tftaIssuer)
	if err != nil {
		panic(err)
	}
	tftMints, _, err := findAccPayments(tftIssuer)
	if err != nil {
		panic(err)
	}

	fmt.Println("==========")
	fmt.Println("TFTA mints")
	fmt.Println("==========")

	for _, tftaMint := range tftaMints {
		isDeauth, err := isDeauthHash(tftaMint.memo)
		if err != nil {
			panic(err)
		}
		if isDeauth {
			continue
		}
		fmt.Printf("%s,%s,%s,%s,%s\n", tftaMint.txID, tftaMint.ts.Format(time.RFC822), tftaMint.to, stropesToString(tftaMint.amount), tftaMint.memo)
	}

	fmt.Println("==========")
	fmt.Println("TFT mints")
	fmt.Println("==========")

	for _, tftMint := range tftMints {
		isConverstion := false
		for _, tftaBurn := range tftaBurns {
			if tftMint.memo == tftaBurn.txID {
				isConverstion = true
				break
			}
		}
		if isConverstion {
			continue
		}
		isDeauth, err := isDeauthHash(tftMint.memo)
		if err != nil {
			panic(err)
		}
		if isDeauth {
			continue
		}
		fmt.Printf("%s,%s,%s,%s,%s\n", tftMint.txID, tftMint.ts.Format(time.RFC822), tftMint.to, stropesToString(tftMint.amount), tftMint.memo)
	}
}

// returns mints and burns
func findAccPayments(account string) ([]mint, []burn, error) {
	client := horizonclient.DefaultPublicNetClient

	mints := []mint{}
	burns := []burn{}

	cursor := ""
	for {
		opReq := horizonclient.OperationRequest{
			ForAccount: account,
			Cursor:     cursor,
			Limit:      200,
			Join:       "transactions",
		}
		ops, err := client.Operations(opReq)
		if err != nil {
			e := err.(*horizonclient.Error)
			fmt.Println(e.Problem)
			return nil, nil, err
		}

		if len(ops.Embedded.Records) == 0 {
			break
		}
		cursor = ops.Embedded.Records[len(ops.Embedded.Records)-1].PagingToken()
		for _, op := range ops.Embedded.Records {
			fmt.Fprintf(os.Stderr, "Processing transaction %s", op.GetTransactionHash())
			if payment, ok := op.(operations.Payment); ok {
				am, err := stellarStringToStropes(payment.Amount)
				if err != nil {
					return nil, nil, err
				}
				if payment.To == account {
					burns = append(burns, burn{
						txID:   payment.TransactionHash,
						ts:     payment.LedgerCloseTime,
						from:   payment.From,
						amount: am,
					})
					continue
				}
				memo := ""
				if payment.Transaction != nil {
					if payment.Transaction.MemoType != "hash" {
						// All minting txes have a "hash" memo type
						continue
					}
					raw, err := base64.StdEncoding.DecodeString(payment.Transaction.Memo)
					if err != nil {
						return nil, nil, err
					}
					memo = hex.EncodeToString(raw)
				}
				mints = append(mints, mint{
					txID:   payment.TransactionHash,
					ts:     payment.LedgerCloseTime,
					to:     payment.To,
					amount: am,
					memo:   memo,
				})
			}
		}
		if len(ops.Embedded.Records) < 200 {
			break
		}
	}

	return mints, burns, nil
}

func isDeauthHash(hash string) (bool, error) {
	res, err := http.Get(fmt.Sprintf("https://explorer2.threefoldtoken.com/explorer/hashes/%s", hash))
	if err != nil {
		return false, errors.Wrap(err, "could not fetch hash on explorer")
	}
	return res.StatusCode == 200, nil
}

func getRivineMints() ([]rivineMint, error) {
	client := http.DefaultClient
	mints := []rivineMint{}
	for i := 1; i < 700000; i++ {
		fmt.Fprintf(os.Stderr, "\rProcessing block %d", i)
		blockData, err := client.Get(fmt.Sprintf("https://explorer2.threefoldtoken.com/explorer/blocks/%d", i))
		if err != nil {
			return nil, errors.Wrap(err, "could not get explorer block")
		}
		if blockData.StatusCode != 200 {
			return nil, fmt.Errorf("invalid response code %d when loading block %d", blockData.StatusCode, i)
		}
		block := api.ExplorerBlockGET{}
		if err = json.NewDecoder(blockData.Body).Decode(&block); err != nil {
			return nil, errors.Wrap(err, "could not decode block")
		}
		for _, tx := range block.Block.Transactions {
			if tx.RawTransaction.Version != TransactionVersionMinterDefinition {
				continue
			}
			// it's a mint
			for _, co := range tx.RawTransaction.CoinOutputs {
				val, err := co.Value.Uint64()
				if err != nil {
					return nil, errors.Wrap(err, "could not interpret value of co")
				}
				mints = append(mints, rivineMint{
					txID:   tx.ID.String(),
					ts:     time.Unix(int64(tx.Timestamp), 0),
					to:     co.Condition.UnlockHash().String(),
					amount: val,
					memo:   hex.EncodeToString(tx.RawTransaction.ArbitraryData),
				})
			}
		}
	}

	return mints, nil
}

func stropesToString(stropes uint64) string {
	return fmt.Sprintf("%d.%d", stropes/10000000, stropes%10000000)
}

func rivineToString(amount uint64) string {
	return fmt.Sprintf("%d.%d", amount/1000000000, amount%1000000000)
}

func stellarStringToStropes(amount string) (uint64, error) {
	parts := strings.Split(amount, ".")
	value, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, errors.Wrap(err, "could not parse amount")
	}
	value *= 10000000
	if len(parts) > 1 {
		v, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return 0, errors.Wrap(err, "could not parse amount")
		}
		value += v
	}
	return value, nil
}
