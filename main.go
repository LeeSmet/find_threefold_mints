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

// if a transaction is 7 days 4 hours later than the previous one, consider it a new
// cluster. This is chosen since there is an outlier which is just covered by this,
// but with this time the payout clusters are still properly separated.
const clusterCutoff = time.Hour * 172

type rivineMint struct {
	txID   string
	ts     time.Time
	to     string
	amount uint64
	memo   string
}

func (rm rivineMint) toGeneral() generalMint {
	// these are the same, as we use the same precision
	return generalMint(rm)
}

// mint on the stellar network.
type mint struct {
	txID   string
	ts     time.Time
	to     string
	amount uint64
	memo   string
}

func (m mint) toGeneral() generalMint {
	return generalMint {
		txID: m.txID,
		ts: m.ts,
		to: m.to,
		amount: m.amount * 100,
		memo: m.memo,
	}
}

type burn struct {
	txID   string
	ts     time.Time
	from   string
	amount uint64
}

// generalMint is mint info in a network independant format. Specifically, the
// "amount" is expressed with 9 digits precision
type generalMint struct {
	txID string
	ts time.Time
	to string
	// amount with 9 digits precision
	amount uint64
	memo string
}

// print the amount of tokens minted as a string.
func (gm generalMint) stringAmount() string {
	return fmt.Sprintf("%d.%09d", gm.amount / 1000000000, gm.amount % 1000000000)
}

// payoutCluster of mint txes which should have occurred as a result of the same
// period
type payoutCluster struct {
	start time.Time
	end time.Time
	transactions uint
	recipients map[string]struct{}
	amount uint64
}

// addMint to the payoutCluster. Returns if the transaction fits in the
// cluster. If it does not fit, the cluster is not updated.
// It is the callers responsibility to ensure the same mint is only added once.
func (pc *payoutCluster) addMint(gm generalMint) bool {
	if gm.ts.After(pc.end.Add(clusterCutoff)) {
		return false
	}
	// mint is sufficiently close to the last one in the cluster
	pc.end = gm.ts
	pc.transactions += 1
	pc.recipients[gm.to] = struct{}{}
	pc.amount += gm.amount
	return true
}

func (pc payoutCluster) stringAmount() string {
	return fmt.Sprintf("%d.%09d", pc.amount / 1000000000, pc.amount % 1000000000)
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
	tftaMints, tftaBurns, err := findAccPayments(tftaIssuer)
	if err != nil {
		panic(err)
	}
	tftMints, _, err := findAccPayments(tftIssuer)
	if err != nil {
		panic(err)
	}
	gms := []generalMint{}

	for _, rmint := range rivMints {
		gms = append(gms, rmint.toGeneral())
	}

	for _, tftaMint := range tftaMints {
		isDeauth, err := isDeauthHash(tftaMint.memo)
		if err != nil {
			panic(err)
		}
		if isDeauth {
			continue
		}
		gms = append(gms, tftaMint.toGeneral())
	}

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
		gms = append(gms, tftMint.toGeneral())
	}

	f, err := os.Create("all_mints.csv")
	if err != nil {
		panic(err)
	}
	f.WriteString("Trasaction ID,Transaction time,Recipient,Amount,Memo\n")
	for _, gm := range gms {
		f.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s\n",gm.txID, gm.ts.Format(time.RFC822),gm.to,gm.stringAmount(),gm.memo))
	}
	if err = f.Close(); err != nil {
		panic(err)
	}

	// Now cluster payouts per cycle. Very fancy clustering algorithm ( ???? ???? ????)
	// Note that due to the way the data set is constructed, it is already sorted
	// on timestamp.
	clusters := []payoutCluster{}
	for _, gm := range gms {
		// If there is no previous cluster, or the mint does not fit in the previous
		// cluster, create a new one.
		if len(clusters) == 0 || !clusters[len(clusters)-1].addMint(gm) {
			clusters = append(clusters, payoutCluster {
				start: gm.ts,
				end: gm.ts,
				transactions: 1,
				recipients: map[string]struct{}{gm.to: {}},
				amount: gm.amount,
			})
		}
	}


	f, err = os.Create("cluster_mints.csv")
	if err != nil {
		panic(err)
	}
	f.WriteString("Cluster start,Cluster end,Transaction count,Unique recipients,Amount\n")
	for _, c := range clusters {
		f.WriteString(fmt.Sprintf("%s,%s,%d,%d,%s\n",c.start.Format(time.RFC822), c.end.Format(time.RFC822),c.transactions,len(c.recipients),c.stringAmount()))
	}
	if err = f.Close(); err != nil {
		panic(err)
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

func isDeauthHash(hash string) (bool, error) {
	res, err := http.Get(fmt.Sprintf("https://explorer2.threefoldtoken.com/explorer/hashes/%s", hash))
	if err != nil {
		return false, errors.Wrap(err, "could not fetch hash on explorer")
	}
	return res.StatusCode == 200, nil
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
