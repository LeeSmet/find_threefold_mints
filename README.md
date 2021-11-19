# Find threefold mints

A tool to generate an overview of all mints done by threefold, on both the old
rivine based tfchain, and on stellar. For every mint, a transaction ID is printed,
along with the receiver, amount, time of execution, and the memo.

## Process

This tool aims to find all mints performed by threefold. We distinguish between 3
tokens which have been minted:

- rivine based TFT
- stellar based TFTA
- stellar based TFT

Since we moved between different blockchains, all tokens on stellar are technically
newly minted tokens. However since these already exit on an old blockchain, that
would give a wrong impression. To be clear, a mint is defined as a generation of
new, previously unexisting tokens, as a result of connecting capacity to the threefold
grid. To this end, we must detect and differentiate, and ultimately remove, tokens
minted as result of transfers or token exchanges.

## Rivine based tfchain mints

On the old tfchain, tokens were only minted as a result of attaching capacity (
note that every block created also produced 1 TFT as block creator reward). For
this purpose, a separate transaction was introduced (transaction version 129). As
such, we can simply iterate over all blocks on the network, then iterate over every
transaction in the block, and if we spot a minting transaction, the relevant info
is extracted. We don't care about the 1 TFT block creator reward, as this is the
result of the actual block, and not of a transaction, so this is automatically
ignored. It is possible to do so from the raw boltdb database files of an explorer
(as both the consensus set db and explorer db are needed to extract the required
info). Since adding those files in this repo would be a big bloat, and more importantly,
since it's been over 2 years since I worked on that project and as such my knowledge
of the codebase has decreased to the point were I can't just wire that up without
looking at the docs/actual code, a different approach is used. A tfchain explorer
is still live at https://explorer2.threefoldtoken.com. Using this, we can simply
query all the blocks from the explorer. This pulls all the blocks over network, and
those blocks will have significant amount of redundant data (we need to pull every
block, every tx, and some information is redundantly calculated/included over txes).
Still, it allows for a very straightforward solution. Note that tfchain no longer
produces blocks, and even if it did, no more mints happen there, so we have a finite
list of blocks we need to check in the end.

## Stellar based TFTA

When threefold migrated to the stellar blockchain, 2 currencies (called assets) were
introduced: TFT and TFTA. TFTA was used for people who transfered their unlocked
tokens, and locked tokens which unlocked before a certain date (1st of jan 2021).
Additionally, until this time, the new V2 minting also happend in TFTA. As a result,
there can be 2 causes for TFTA mints:

- transfer tokens for the rivine based tfchain
- actual minting as a result of capacity being connected

In both cases, the transaction caries a `memo_hash`. In case of a transfer, the
hash is the transaction ID on the rivine based tfchain which deauthorized the address
for which the token are bieng transfered. In case of a capacity mint, the hash
is the hash of the minting receipt, which can be looked up on [the minting UI](https://minting.threefold.io).
Since TFTA can also be converted to TFT, we need to keep track of all the burns
(returning an asset to the issuing account on stellar), for the TFT on stellar minting.
We can thus proceed by listing all operations on stellar for the TFTA issuer. Then
check if the issuer is the `from` address (which is a mint), or the to address
(which is a burn). For burns, keep track of the txID mostly. For mints, we then
check if the memo (as hex) is actually a deauthorization transaction on the old
rivine based tfchain. If it is, then this mint is ignored as it is the result of
a token transfer (and as such, the tokens already existed). If it is not, this is
an actual capacity mint.

## Stellar based TFT

TFT on stellar behaves much like TFTA on stellar. Is was created for all locked
tokens which unlocked after 1st of jan 2021. Starting in 2021, it is also used
for minting as a result of capacity being added to the threefold grid. Lastly, it
is possible to convert TFTA to TFT, simply by sending the TFTA back to the issuer
(burning them). So there are 3 possible causes of a TFT mint:

- transfer locked tokens unlocking after 1st of jan 2021
- actual minting as a result of capacity being used
- conversion of legacy TFTA to TFT

Once again, all these cases have a `memo_hash` filled in. The process is thus the
same as for TFTA. However, after we verified that these tokens are not the result
of a transfer (by checking the old tfchain explorer with the memo), we need to verify
that the tokens are not the result of a TFTA conversion. To this end, we kept the
list of TFTA burns in the previous step. We iterate over all burns, and if we find
one where the transaction ID matches the memo of our mint, then it is in fact a
conversion of TFTA, and we don't have to account for this operation (we remove it).
This leaves us with a list of actual mints.

## Notes on output

Minting happens once every period, and a period corresponds roughly to a month.
While it is possible that 2 minting cycles happen in 1 month (begining and end),
all transactions in the output should be clustered. There should not be any outliers.
