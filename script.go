// Copyright (c) 2013 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package btcscript

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/conformal/btcwire"
	"github.com/davecgh/go-spew/spew"
	"time"
)

// StackErrShortScript is returned if the script has an opcode that is too long
// for the length of the script.
var StackErrShortScript = errors.New("execute past end of script")

// StackErrUnderflow is returned if an opcode requires more items on the stack
// than is present.
var StackErrUnderflow = errors.New("stack underflow")

// StackErrInvalidArgs is returned if the argument for an opcode is out of
// acceptable range.
var StackErrInvalidArgs = errors.New("invalid argument")

// StackErrOpDisabled is returned when a disabled opcode is encountered in the
// script.
var StackErrOpDisabled = errors.New("Disabled Opcode")

// StackErrVerifyFailed is returned when one of the OP_VERIFY or OP_*VERIFY
// instructions is executed and the conditions fails.
var StackErrVerifyFailed = errors.New("Verify failed")

// StackErrNumberTooBig is returned when the argument for an opcode that should
// be an offset is obviously far too large.
var StackErrNumberTooBig = errors.New("number too big")

// StackErrInvalidOpcode is returned when an opcode marked as invalid or a
// completely undefined opcode is encountered.
var StackErrInvalidOpcode = errors.New("Invalid Opcode")

// StackErrReservedOpcode is returned when an opcode marked as reserved is
// encountered.
var StackErrReservedOpcode = errors.New("Reserved Opcode")

// StackErrEarlyReturn is returned when OP_RETURN is exectured in the script.
var StackErrEarlyReturn = errors.New("Script returned early")

// StackErrNoIf is returned if an OP_ELSE or OP_ENDIF is encountered without
// first having an OP_IF or OP_NOTIF in the script.
var StackErrNoIf = errors.New("OP_ELSE or OP_ENDIF with no matching OP_IF")

// StackErrMissingEndif is returned if the end of a script is reached without
// and OP_ENDIF to correspond to a conditional expression.
var StackErrMissingEndif = fmt.Errorf("execute fail, in conditional execution")

// StackErrTooManyPubkeys is returned if an OP_CHECKMULTISIG is encountered
// with more than MaxPubKeysPerMultiSig pubkeys present.
var StackErrTooManyPubkeys = errors.New("Invalid pubkey count in OP_CHECKMULTISIG")

// StackErrTooManyOperations is returned if a script has more then
// MaxOpsPerScript opcodes that do not push data.
var StackErrTooManyOperations = errors.New("Too many operations in script")

// StackErrElementTooBig is returned if the size of an element to be pushed to
// the stack is over MaxScriptElementSize.
var StackErrElementTooBig = errors.New("Element in script too large")

// StackErrUnknownAddress is returned when ScriptToAddress does not recognise
// the pattern of the script and thus can not find the address for payment.
var StackErrUnknownAddress = fmt.Errorf("non-recognised address")

// StackErrScriptFailed is returned when at the end of a script the boolean
// on top of the stack is false signifying that the script has failed.
var StackErrScriptFailed = fmt.Errorf("execute fail, fail on stack")

// Bip16Activation is the timestamp where BIP0016 is valid to use in the
// blockchain.  To be used to determine if BIP0016 should be called for or not.
// This timestamp corresponds to Sun Apr 1 00:00:00 UTC 2012.
var Bip16Activation = time.Unix(1333238400, 0)

// Hash type bits from the end of a signature.
const (
	SigHashOld          = 0x0
	SigHashAll          = 0x1
	SigHashNone         = 0x2
	SigHashSingle       = 0x3
	SigHashAnyOneCanPay = 0x80
)

// These are the constants specified for maximums in individual scripts.
const (
	MaxOpsPerScript       = 201 // Max number of non-push operations.
	MaxPubKeysPerMultiSig = 20  // Multisig can't have more sigs than this.
	MaxScriptElementSize  = 520 // Max bytes pushable to the stack.
)

// ScriptType is an enumeration for the list of standard types of script.
type scriptType byte

// Types of script payment known about in the blockchain.
const (
	pubKeyTy      scriptType = iota // Pay pubkey.
	pubKeyHashTy                    // Pay pubkey hash.
	scriptHashTy                    // Pay to script hash.
	multiSigTy                      // Multi signature.
	nonStandardTy                   // None of the above.
)

// Script is the virtual machine that executes btcscripts.
type Script struct {
	scripts         [][]parsedOpcode
	scriptidx       int
	scriptoff       int
	lastcodesep     int
	dstack          Stack // data stack
	astack          Stack // alt stack
	tx              btcwire.MsgTx
	txidx           int
	pver            uint32
	condStack       []int
	numOps          int
	bip16           bool     // treat execution as pay-to-script-hash
	savedFirstStack [][]byte // stack from first script for bip16 scripts
}

// isPubkey returns true if the script passed is a pubkey transaction, false
// otherwise.
func isPubkey(pops []parsedOpcode) bool {
	return len(pops) == 2 &&
		pops[0].opcode.value > OP_FALSE &&
		pops[0].opcode.value <= OP_DATA_75 &&
		pops[1].opcode.value == OP_CHECKSIG
}

// isPubkeyHash returns true if the script passed is a pubkey hash transaction,
// false otherwise.
func isPubkeyHash(pops []parsedOpcode) bool {
	return len(pops) == 5 &&
		pops[0].opcode.value == OP_DUP &&
		pops[1].opcode.value == OP_HASH160 &&
		pops[2].opcode.value == OP_DATA_20 &&
		pops[3].opcode.value == OP_EQUALVERIFY &&
		pops[4].opcode.value == OP_CHECKSIG

}

// isScriptHash returns true if the script passed is a pay-to-script-hash (P2SH)
// transction, false otherwise.
func isScriptHash(pops []parsedOpcode) bool {
	return len(pops) == 3 &&
		pops[0].opcode.value == OP_HASH160 &&
		pops[1].opcode.value == OP_DATA_20 &&
		pops[2].opcode.value == OP_EQUAL
}

// isMultiSig returns true if the passed script is a multisig transaction, false
// otherwise.
func isMultiSig(pops []parsedOpcode) bool {
	l := len(pops)
	// absolute minimum is 1 pubkey so
	// OP_1-16, pubkey, OP_1, OP_CHECK_MULTISIG
	if l < 4 {
		return false
	}
	if pops[0].opcode.value < OP_1 ||
		pops[0].opcode.value > OP_16 {
		return false
	}
	if pops[l-2].opcode.value < OP_1 ||
		pops[l-2].opcode.value > OP_16 {
		return false
	}
	if pops[l-1].opcode.value != OP_CHECK_MULTISIG {
		return false
	}
	for _, pop := range pops[1 : l-2] {
		// valid pubkeys are either 65 or 33 bytes
		if len(pop.data) != 33 &&
			len(pop.data) != 65 {
			return false
		}
	}
	return true
}

// isPushOnly returns true if the script only pushes data, false otherwise.
func isPushOnly(pops []parsedOpcode) bool {
	// technically we cheat here, we don't look at opcodes
	for _, pop := range pops {
		// all opcodes up to OP_16 are data instructions.
		if pop.opcode.value < OP_FALSE ||
			pop.opcode.value > OP_16 {
			return false
		}
	}
	return true
}

// scriptType returns the type of the script being inspected from the known
// standard types.
func typeOfScript(pops []parsedOpcode) scriptType {
	// XXX dubious optimisation: order these in order of popularity in the
	// blockchain
	if isPubkey(pops) {
		return pubKeyTy
	} else if isPubkeyHash(pops) {
		return pubKeyHashTy
	} else if isScriptHash(pops) {
		return scriptHashTy
	} else if isMultiSig(pops) {
		return multiSigTy
	}
	return nonStandardTy

}

// parseScript preparses the script in bytes into a list of parsedOpcodes while
// applying a number of sanity checks.
func parseScript(script []byte) ([]parsedOpcode, error) {
	retScript := []parsedOpcode{}
	for i := 0; i < len(script); {
		instr := script[i]
		op, ok := opcodemap[instr]
		if !ok {
			return nil, StackErrInvalidOpcode
		}
		pop := parsedOpcode{opcode: op}
		// parse data out of instruction.
		switch {
		case op.length == 1:
			// no data, done here
			i++
		case op.length > 1:
			if len(script[i:]) < op.length {
				return nil, StackErrShortScript
			}
			// slice out the data.
			pop.data = script[i+1 : i+op.length]
			i += op.length
		case op.length < 0:
			var err error
			var l uint
			off := i + 1
			switch op.length {
			case -1:
				l, err = scriptUInt8(script[off:])
			case -2:
				l, err = scriptUInt16(script[off:])
			case -4:
				l, err = scriptUInt32(script[off:])
			default:
				return nil, fmt.Errorf("invalid opcode length %d", op.length)
			}

			if err != nil {
				return nil, err
			}
			off = i + 1 - op.length // beginning of data
			if int(l) > len(script[off:]) {
				return nil, StackErrShortScript
			}
			if l > MaxScriptElementSize {
				return nil, StackErrElementTooBig
			}
			pop.data = script[off : off+int(l)]
			i += 1 - op.length + int(l)
		}
		retScript = append(retScript, pop)
	}
	return retScript, nil
}

// unparseScript reversed the action of parseScript and returns the
// parsedOpcodes as a list of bytes
func unparseScript(pops []parsedOpcode) []byte {
	script := []byte{}
	for _, pop := range pops {
		script = append(script, pop.bytes()...)
	}
	return script
}

// NewScript returns a new script engine for the provided tx and input idx with
// a signature script scriptSig and a pubkeyscript scriptPubKey. If bip16 is
// true then it will be treated as if the bip16 threshhold has passed and thus
// pay-to-script hash transactions will be fully validated.
func NewScript(scriptSig []byte, scriptPubKey []byte, txidx int, tx *btcwire.MsgTx, pver uint32, bip16 bool) (*Script, error) {
	var m Script
	scripts := [][]byte{scriptSig, scriptPubKey}
	m.scripts = make([][]parsedOpcode, len(scripts))
	for i, scr := range scripts {
		var err error
		m.scripts[i], err = parseScript(scr)
		if err != nil {
			return nil, err
		}

		// if the first scripts(s) are empty, must set the PC to the next script.
		if len(scr) == 0 {
			// yes this could end up setting to an invalid intial PC if all scripts were empty
			m.scriptidx = i + 1
		}
	}

	if bip16 && isScriptHash(m.scripts[1]) {
		// if we are pay to scripthash then we only accept input
		// scripts that push data
		if !isPushOnly(m.scripts[0]) {
			return nil, errors.New("pay to script hash with non " +
				"pushonly input")
		}
		m.bip16 = true
	}

	for i, _ := range tx.TxIn {
		tx.TxIn[i].SignatureScript = []byte{}
	}
	m.tx = *tx
	m.txidx = txidx
	m.pver = pver
	m.condStack = []int{OpCondTrue}

	return &m, nil
}

// Execute will execturte all script in the script engine and return either nil
// for successful validation or an error if one occurred.
func (s *Script) Execute() (err error) {
	done := false
	for done != true {
		log.Tracef("%v", newLogClosure(func() string {
			dis, err := s.DisasmPC()
			if err != nil {
				return fmt.Sprintf("stepping (%v)", err)
			}
			return fmt.Sprintf("stepping %v", dis)
		}))

		done, err = s.Step()
		if err != nil {
			return err
		}
		log.Tracef("%v", newLogClosure(func() string {
			var dstr, astr string

			// if we're tracing, dump the stacks.
			if s.dstack.Depth() != 0 {
				dstr = "Stack\n" + spew.Sdump(s.dstack)
			}
			if s.astack.Depth() != 0 {
				astr = "AltStack\n" + spew.Sdump(s.astack)
			}

			return dstr + astr
		}))
	}
	if s.dstack.Depth() < 1 {
		return fmt.Errorf("stack empty at end of execution")
	}
	v, err := s.dstack.PopBool()
	if err == nil && v == false {
		// log interesting data.
		log.Tracef("%v", func() string {
			dis0, _ := s.DisasmScript(0)
			dis1, _ := s.DisasmScript(1)
			return fmt.Sprintf("script0: %s\n script1: %s",
				dis0, dis1)
		})
		err = StackErrScriptFailed
	}
	if err == nil && len(s.condStack) != 1 {
		// conditional execution stack context left active
		err = StackErrMissingEndif
	}

	return err
}

// Step will execute the next instruction and move the program counter to the
// next opcode in the script, or the next script if the curent has ended. Step
// will return true in the case that the last opcode was successfully executed.
// if an error is returned then the result of calling Step or any other method
// is undefined.
func (m *Script) Step() (done bool, err error) {
	// verify that it is pointing to a valid script address
	err = m.validPC()
	if err != nil {
		return
	}
	opcode := m.scripts[m.scriptidx][m.scriptoff]

	executeInstr := true
	if m.condStack[0] != OpCondTrue {
		// some opcodes still 'activate' if on the non-executing side
		// of conditional execution
		if opcode.conditional() {
			executeInstr = true
		} else {
			executeInstr = false
		}
	}
	if executeInstr {
		err = opcode.exec(m)
		if err != nil {
			return
		}
	}

	// prepare for next instruction
	m.scriptoff++
	if m.scriptoff >= len(m.scripts[m.scriptidx]) {
		// should only be == from checks before
		m.scriptoff = 0
		if m.scriptidx == 0 && m.bip16 {
			m.savedFirstStack = m.GetStack()
		} else if m.scriptidx == 1 && m.bip16 {
			// Check for successful completion of script
			v, err := m.dstack.PopBool()
			if err != nil {
				return false, err
			}
			if v == false {
				return false, StackErrScriptFailed
			}
			// check that first element on stack is a bool
			script := m.savedFirstStack[len(m.savedFirstStack)-1]
			pops, err := parseScript(script)
			if err != nil {
				return false, err
			}
			m.scripts = append(m.scripts, pops)
			// Set stack to be the stack from first script
			// minus the script itself
			m.SetStack(m.savedFirstStack[:len(m.savedFirstStack)-1])
		}
		m.scriptidx++
		// there are zero length scripts in the wild
		if m.scriptidx < len(m.scripts) && m.scriptoff >= len(m.scripts[m.scriptidx]) {
			m.scriptidx++
		}
		m.lastcodesep = 0
		if m.scriptidx >= len(m.scripts) {
			done = true
		}
	}
	return
}

// curPC returns either the current script and offset, or an error if the
// position isn't valid.
func (m *Script) curPC() (script int, off int, err error) {
	err = m.validPC()
	if err != nil {
		return 0, 0, err
	}
	return m.scriptidx, m.scriptoff, nil
}

// validPC returns an error if the current script position is valid for
// execution, nil otherwise.
func (m *Script) validPC() error {
	if m.scriptidx >= len(m.scripts) {
		return fmt.Errorf("Past input scripts %v:%v %v:xxxx", m.scriptidx, m.scriptoff, len(m.scripts))
	}
	if m.scriptoff >= len(m.scripts[m.scriptidx]) {
		return fmt.Errorf("Past input scripts %v:%v %v:%04d", m.scriptidx, m.scriptoff, m.scriptidx, len(m.scripts[m.scriptidx]))
	}
	return nil
}

// DisasmScript returns the disassembly string for the script at offset
// ``idx''.  Where 0 is the scriptSig and 1 is the scriptPubKey.
func (m *Script) DisasmScript(idx int) (disstr string, err error) {
	if idx >= len(m.scripts) {
		return "", fmt.Errorf("Invalid script index")
	}
	for i := range m.scripts[idx] {
		disstr = disstr + m.disasm(idx, i) + "\n"
	}
	return disstr, nil
}

// DisasmPC returns the string for the disassembly of the opcode that will be
// next to execute when Step() is called.
func (m *Script) DisasmPC() (disstr string, err error) {
	scriptidx, scriptoff, err := m.curPC()
	if err != nil {
		return "", err
	}
	return m.disasm(scriptidx, scriptoff), nil
}

// disasm is a helper member to produce the output for DisasmPC and
// DisasmScript. It produces the opcode prefixed by the program counter at the
// provided position in the script. it does no error checking and leaves that
// to the caller to provide a valid offse.
func (m *Script) disasm(scriptidx int, scriptoff int) string {
	return fmt.Sprintf("%02x:%04x: %s", scriptidx, scriptoff,
		m.scripts[scriptidx][scriptoff].print(false))
}

// subScript will return the script since the last OP_CODESEPARATOR
func (s *Script) subScript() []parsedOpcode {
	return s.scripts[s.scriptidx][s.lastcodesep:]
}

// removeOpcode will remove any opcode matching ``opcode'' from the opcode
// stream in pkscript
func removeOpcode(pkscript []parsedOpcode, opcode byte) []parsedOpcode {
	retScript := []parsedOpcode{}
	for _, pop := range pkscript {
		if pop.opcode.value != opcode {
			retScript = append(retScript, pop)
		}
	}
	return retScript
}

// removeOpcodeByData will return the pkscript minus any opcodes that would
// push the data in ``data'' to the stack.
func removeOpcodeByData(pkscript []parsedOpcode, data []byte) []parsedOpcode {
	retScript := []parsedOpcode{}
	for _, pop := range pkscript {
		if !bytes.Equal(pop.data, data) {
			retScript = append(retScript, pop)
		}
	}
	return retScript

}

// DisasmString formats a disassembled script for one line printing.
func DisasmString(buf []byte) (string, error) {
	disbuf := ""
	opcodes, err := parseScript(buf)
	if err != nil {
		return "", err
	}
	for _, pop := range opcodes {
		disbuf += pop.print(true) + " "
	}
	if disbuf != "" {
		disbuf = disbuf[:len(disbuf)-1]
	}
	return disbuf, nil
}

// calcScriptHash will, given the a script and hashtype for the current
// scriptmachine, calculate the doubleSha256 hash of the transaction and
// script to be used for signature signing and verification.
func (s *Script) calcScriptHash(script []parsedOpcode, hashType byte) []byte {

	// remove all instances of OP_CODESEPARATOR still left in the script
	script = removeOpcode(script, OP_CODESEPARATOR)

	// Make a deep copy of the transaction, zeroing out the script
	// for all inputs that are not currently being processed.
	txCopy := s.tx.Copy()
	txidx := s.txidx
	for i := range txCopy.TxIn {
		var txIn btcwire.TxIn
		txIn = *txCopy.TxIn[i]
		txCopy.TxIn[i] = &txIn
		if i == txidx {
			txCopy.TxIn[txidx].SignatureScript =
				unparseScript(script)
		} else {
			txCopy.TxIn[i].SignatureScript = []byte{}
		}
	}
	// Default behaviour has all outputs set up.
	for i := range txCopy.TxOut {
		var txOut btcwire.TxOut
		txOut = *txCopy.TxOut[i]
		txCopy.TxOut[i] = &txOut
	}

	switch hashType & 31 {
	case SigHashNone:
		txCopy.TxOut = txCopy.TxOut[0:0] // empty slice
		for i := range txCopy.TxIn {
			if i != txidx {
				txCopy.TxIn[i].Sequence = 0
			}
		}
	case SigHashSingle:
		// resize output array to up to and including current output
		txCopy.TxOut = txCopy.TxOut[:txidx+1]
		// all but  current output get zeroed out
		for i := 0; i < txidx; i++ {
			txCopy.TxOut[i].Value = -1
			txCopy.TxOut[i].PkScript = []byte{}
		}
		// Sequence on all other inputs is 0, too.
		for i := range txCopy.TxIn {
			if i != txidx {
				txCopy.TxIn[i].Sequence = 0
			}
		}
	default:
		// XXX bitcoind treats undefined hashtypes like normal
		// SigHashAll for purposes of hash generation.
		fallthrough
	case SigHashOld:
		fallthrough
	case SigHashAll:
		// nothing special here
	}
	if hashType&SigHashAnyOneCanPay != 0 {
		txCopy.TxIn = txCopy.TxIn[s.txidx : s.txidx+1]
		txidx = 0
	}

	var wbuf bytes.Buffer
	txCopy.BtcEncode(&wbuf, s.pver)
	// Append LE 4 bytes hash type
	binary.Write(&wbuf, binary.LittleEndian, uint32(hashType))

	return btcwire.DoubleSha256(wbuf.Bytes())
}

// scriptUInt8 return the number stored in the first byte of a slice.
func scriptUInt8(script []byte) (uint, error) {
	if len(script) <= 1 {
		return 0, StackErrShortScript
	}
	return uint(script[0]), nil
}

// scriptUInt16 returns the number stored in the next 2 bytes of a slice.
func scriptUInt16(script []byte) (uint, error) {
	if len(script) <= 2 {
		return 0, StackErrShortScript
	}
	// Yes this is little endian
	return ((uint(script[1]) << 8) | uint(script[0])), nil
}

// scriptUInt32 returns the number stored in the first 4 bytes of a slice.
func scriptUInt32(script []byte) (uint, error) {
	if len(script) <= 4 {
		return 0, StackErrShortScript
	}
	// Yes this is little endian
	return ((uint(script[3]) << 24) | (uint(script[2]) << 16) |
		(uint(script[1]) << 8) | uint(script[0])), nil
}

// getStack returns the contents of stack as a byte array bottom up
func getStack(stack *Stack) [][]byte {
	array := make([][]byte, stack.Depth())
	for i := range array {
		// PeekByteArry can't fail due to overflow, already checked
		array[len(array)-i-1], _ =
			stack.PeekByteArray(i)
	}
	return array
}

// setStack sets the stack to the contents of the array where the last item in
// the array is the top item in the stack.
func setStack(stack *Stack, data [][]byte) {
	// This can not error. Only errors are for invalid arguments.
	_ = stack.DropN(stack.Depth())

	for i := range data {
		stack.PushByteArray(data[i])
	}
}

// GetStack returns the contents of the primary stack as an array. where the
// last item in the array is the top of the stack.
func (s *Script) GetStack() [][]byte {
	return getStack(&s.dstack)
}

// SetStack sets the contents of the primary stack to the contents of the
// provided array where the last item in the array will be the top of the stack.
func (s *Script) SetStack(data [][]byte) {
	setStack(&s.dstack, data)
}

// GetAltStack returns the contents of the primary stack as an array. where the
// last item in the array is the top of the stack.
func (s *Script) GetAltStack() [][]byte {
	return getStack(&s.astack)
}

// SetAltStack sets the contents of the primary stack to the contents of the
// provided array where the last item in the array will be the top of the stack.
func (s *Script) SetAltStack(data [][]byte) {
	setStack(&s.astack, data)
}