// Copyright 2018 The dexon-consensus Authors
// This file is part of the dexon-consensus library.
//
// The dexon-consensus library is free software: you can redistribute it
// and/or modify it under the terms of the GNU Lesser General Public License as
// published by the Free Software Foundation, either version 3 of the License,
// or (at your option) any later version.
//
// The dexon-consensus library is distributed in the hope that it will be
// useful, but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser
// General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the dexon-consensus library. If not, see
// <http://www.gnu.org/licenses/>.

package core

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/dexon-foundation/dexon-consensus/common"
	"github.com/dexon-foundation/dexon-consensus/core/crypto"
	"github.com/dexon-foundation/dexon-consensus/core/crypto/dkg"
	"github.com/dexon-foundation/dexon-consensus/core/crypto/ecdsa"
	"github.com/dexon-foundation/dexon-consensus/core/db"
	"github.com/dexon-foundation/dexon-consensus/core/test"
	"github.com/dexon-foundation/dexon-consensus/core/types"
	typesDKG "github.com/dexon-foundation/dexon-consensus/core/types/dkg"
	"github.com/dexon-foundation/dexon-consensus/core/utils"
)

type ConfigurationChainTestSuite struct {
	suite.Suite

	nIDs    types.NodeIDs
	dkgIDs  map[types.NodeID]dkg.ID
	signers map[types.NodeID]*utils.Signer
	pubKeys []crypto.PublicKey
}

type testCCGlobalReceiver struct {
	s *ConfigurationChainTestSuite

	nodes map[types.NodeID]*configurationChain
	govs  map[types.NodeID]Governance
}

func newTestCCGlobalReceiver(
	s *ConfigurationChainTestSuite) *testCCGlobalReceiver {
	return &testCCGlobalReceiver{
		s:     s,
		nodes: make(map[types.NodeID]*configurationChain),
		govs:  make(map[types.NodeID]Governance),
	}
}

func (r *testCCGlobalReceiver) ProposeDKGComplaint(
	complaint *typesDKG.Complaint) {
	for _, gov := range r.govs {
		gov.AddDKGComplaint(complaint.Round, test.CloneDKGComplaint(complaint))
	}
}

func (r *testCCGlobalReceiver) ProposeDKGMasterPublicKey(
	mpk *typesDKG.MasterPublicKey) {
	for _, gov := range r.govs {
		gov.AddDKGMasterPublicKey(mpk.Round, test.CloneDKGMasterPublicKey(mpk))
	}
}

func (r *testCCGlobalReceiver) ProposeDKGPrivateShare(
	prv *typesDKG.PrivateShare) {
	go func() {
		receiver, exist := r.nodes[prv.ReceiverID]
		if !exist {
			panic(errors.New("should exist"))
		}
		if err := receiver.processPrivateShare(prv); err != nil {
			panic(err)
		}
	}()
}

func (r *testCCGlobalReceiver) ProposeDKGAntiNackComplaint(
	prv *typesDKG.PrivateShare) {
	go func() {
		for _, cc := range r.nodes {
			if err := cc.processPrivateShare(
				test.CloneDKGPrivateShare(prv)); err != nil {
				panic(err)
			}
		}
	}()
}

func (r *testCCGlobalReceiver) ProposeDKGMPKReady(ready *typesDKG.MPKReady) {
	for _, gov := range r.govs {
		gov.AddDKGMPKReady(ready.Round, test.CloneDKGMPKReady(ready))
	}
}

func (r *testCCGlobalReceiver) ProposeDKGFinalize(final *typesDKG.Finalize) {
	for _, gov := range r.govs {
		gov.AddDKGFinalize(final.Round, test.CloneDKGFinalize(final))
	}
}

type testCCReceiver struct {
	signer *utils.Signer
	recv   *testCCGlobalReceiver
}

func newTestCCReceiver(nID types.NodeID, recv *testCCGlobalReceiver) *testCCReceiver {
	return &testCCReceiver{
		signer: recv.s.signers[nID],
		recv:   recv,
	}
}

func (r *testCCReceiver) ProposeDKGComplaint(
	complaint *typesDKG.Complaint) {
	if err := r.signer.SignDKGComplaint(complaint); err != nil {
		panic(err)
	}
	r.recv.ProposeDKGComplaint(complaint)
}

func (r *testCCReceiver) ProposeDKGMasterPublicKey(
	mpk *typesDKG.MasterPublicKey) {
	if err := r.signer.SignDKGMasterPublicKey(mpk); err != nil {
		panic(err)
	}
	r.recv.ProposeDKGMasterPublicKey(mpk)
}

func (r *testCCReceiver) ProposeDKGPrivateShare(
	prv *typesDKG.PrivateShare) {
	if err := r.signer.SignDKGPrivateShare(prv); err != nil {
		panic(err)
	}
	r.recv.ProposeDKGPrivateShare(prv)
}

func (r *testCCReceiver) ProposeDKGAntiNackComplaint(
	prv *typesDKG.PrivateShare) {
	// We would need to propose anti nack complaint for private share from
	// others. Only sign those private shares with zero length signature.
	if len(prv.Signature.Signature) == 0 {
		if err := r.signer.SignDKGPrivateShare(prv); err != nil {
			panic(err)
		}
	}
	r.recv.ProposeDKGAntiNackComplaint(prv)
}

func (r *testCCReceiver) ProposeDKGMPKReady(ready *typesDKG.MPKReady) {
	if err := r.signer.SignDKGMPKReady(ready); err != nil {
		panic(err)
	}
	r.recv.ProposeDKGMPKReady(ready)
}

func (r *testCCReceiver) ProposeDKGFinalize(final *typesDKG.Finalize) {
	if err := r.signer.SignDKGFinalize(final); err != nil {
		panic(err)
	}
	r.recv.ProposeDKGFinalize(final)
}

func (s *ConfigurationChainTestSuite) setupNodes(n int) {
	s.nIDs = make(types.NodeIDs, 0, n)
	s.signers = make(map[types.NodeID]*utils.Signer, n)
	s.dkgIDs = make(map[types.NodeID]dkg.ID)
	s.pubKeys = nil
	ids := make(dkg.IDs, 0, n)
	for i := 0; i < n; i++ {
		prvKey, err := ecdsa.NewPrivateKey()
		s.Require().NoError(err)
		nID := types.NewNodeID(prvKey.PublicKey())
		s.nIDs = append(s.nIDs, nID)
		s.signers[nID] = utils.NewSigner(prvKey)
		s.pubKeys = append(s.pubKeys, prvKey.PublicKey())
		id := dkg.NewID(nID.Hash[:])
		ids = append(ids, id)
		s.dkgIDs[nID] = id
	}
}

func (s *ConfigurationChainTestSuite) runDKG(
	k, n int, round uint64) map[types.NodeID]*configurationChain {
	s.setupNodes(n)

	cfgChains := make(map[types.NodeID]*configurationChain)
	recv := newTestCCGlobalReceiver(s)

	for _, nID := range s.nIDs {
		gov, err := test.NewGovernance(test.NewState(DKGDelayRound,
			s.pubKeys, 100*time.Millisecond, &common.NullLogger{}, true,
		), ConfigRoundShift)
		s.Require().NoError(err)
		cache := utils.NewNodeSetCache(gov)
		dbInst, err := db.NewMemBackedDB()
		s.Require().NoError(err)
		cfgChains[nID] = newConfigurationChain(nID,
			newTestCCReceiver(nID, recv), gov, cache, dbInst,
			&common.NullLogger{})
		recv.nodes[nID] = cfgChains[nID]
		recv.govs[nID] = gov
	}

	for _, cc := range cfgChains {
		cc.registerDKG(round, k)
	}

	for _, gov := range recv.govs {
		s.Require().Len(gov.DKGMasterPublicKeys(round), n)
	}

	errs := make(chan error, n)
	wg := sync.WaitGroup{}
	wg.Add(n)
	for _, cc := range cfgChains {
		go func(cc *configurationChain) {
			defer wg.Done()
			errs <- cc.runDKG(round)
		}(cc)
	}
	wg.Wait()
	for range cfgChains {
		s.Require().NoError(<-errs)
	}
	return cfgChains
}

func (s *ConfigurationChainTestSuite) preparePartialSignature(
	hash common.Hash,
	round uint64,
	cfgChains map[types.NodeID]*configurationChain) (
	psigs []*typesDKG.PartialSignature) {
	psigs = make([]*typesDKG.PartialSignature, 0, len(cfgChains))
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round]; !exist {
			continue
		}
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			continue
		}
		psig, err := cc.preparePartialSignature(round, hash)
		s.Require().NoError(err)
		signer, exist := s.signers[cc.ID]
		s.Require().True(exist)
		err = signer.SignDKGPartialSignature(psig)
		s.Require().NoError(err)
		psigs = append(psigs, psig)
	}
	return
}

// TestConfigurationChain will test the entire DKG+TISG protocol including
// exchanging private shares, recovering share secret, creating partial sign and
// recovering threshold signature.
// All participants are good people in this test.
func (s *ConfigurationChainTestSuite) TestConfigurationChain() {
	k := 4
	n := 10
	round := DKGDelayRound
	cfgChains := s.runDKG(k, n, round)

	hash := crypto.Keccak256Hash([]byte("🌚🌝"))
	psigs := s.preparePartialSignature(hash, round, cfgChains)

	tsigs := make([]crypto.Signature, 0, n)
	errs := make(chan error, n)
	tsigChan := make(chan crypto.Signature, n)
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round]; !exist {
			continue
		}
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			continue
		}
		go func(cc *configurationChain) {
			tsig, err := cc.runTSig(round, hash)
			// Prevent racing by collecting errors and check in main thread.
			errs <- err
			tsigChan <- tsig
		}(cc)
		for _, psig := range psigs {
			err := cc.processPartialSignature(psig)
			s.Require().NoError(err)
		}
	}
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round]; !exist {
			s.FailNow("Should be qualifyied")
		}
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			s.FailNow("Should be qualifyied")
		}
		s.Require().NoError(<-errs)
		tsig := <-tsigChan
		for _, prevTsig := range tsigs {
			s.Equal(prevTsig, tsig)
		}
	}
}

func (s *ConfigurationChainTestSuite) TestDKGMasterPublicKeyDelayAdd() {
	k := 4
	n := 10
	round := DKGDelayRound
	lambdaDKG := 1000 * time.Millisecond
	s.setupNodes(n)

	cfgChains := make(map[types.NodeID]*configurationChain)
	recv := newTestCCGlobalReceiver(s)
	delayNode := s.nIDs[0]

	for _, nID := range s.nIDs {
		state := test.NewState(DKGDelayRound,
			s.pubKeys, 100*time.Millisecond, &common.NullLogger{}, true)
		gov, err := test.NewGovernance(state, ConfigRoundShift)
		s.Require().NoError(err)
		s.Require().NoError(state.RequestChange(
			test.StateChangeLambdaDKG, lambdaDKG))
		cache := utils.NewNodeSetCache(gov)
		dbInst, err := db.NewMemBackedDB()
		s.Require().NoError(err)
		cfgChains[nID] = newConfigurationChain(
			nID, newTestCCReceiver(nID, recv), gov, cache, dbInst,
			&common.NullLogger{})
		recv.nodes[nID] = cfgChains[nID]
		recv.govs[nID] = gov
	}

	for nID, cc := range cfgChains {
		if nID == delayNode {
			continue
		}
		cc.registerDKG(round, k)
	}
	time.Sleep(lambdaDKG)
	cfgChains[delayNode].registerDKG(round, k)

	for _, gov := range recv.govs {
		s.Require().Len(gov.DKGMasterPublicKeys(round), n-1)
	}

	errs := make(chan error, n)
	wg := sync.WaitGroup{}
	wg.Add(n)
	for _, cc := range cfgChains {
		go func(cc *configurationChain) {
			defer wg.Done()
			errs <- cc.runDKG(round)
		}(cc)
	}
	wg.Wait()
	for range cfgChains {
		s.Require().NoError(<-errs)
	}
	for nID, cc := range cfgChains {
		shouldExist := nID != delayNode
		_, exist := cc.npks[round]
		s.Equal(shouldExist, exist)
		if !exist {
			continue
		}
		_, exist = cc.npks[round].QualifyNodeIDs[nID]
		s.Equal(shouldExist, exist)
	}
}

func (s *ConfigurationChainTestSuite) TestDKGComplaintDelayAdd() {
	k := 4
	n := 10
	round := DKGDelayRound
	lambdaDKG := 1000 * time.Millisecond
	s.setupNodes(n)

	cfgChains := make(map[types.NodeID]*configurationChain)
	recv := newTestCCGlobalReceiver(s)
	recvs := make(map[types.NodeID]*testCCReceiver)
	for _, nID := range s.nIDs {
		state := test.NewState(DKGDelayRound,
			s.pubKeys, 100*time.Millisecond, &common.NullLogger{}, true)
		gov, err := test.NewGovernance(state, ConfigRoundShift)
		s.Require().NoError(err)
		s.Require().NoError(state.RequestChange(
			test.StateChangeLambdaDKG, lambdaDKG))
		cache := utils.NewNodeSetCache(gov)
		dbInst, err := db.NewMemBackedDB()
		s.Require().NoError(err)
		recvs[nID] = newTestCCReceiver(nID, recv)
		cfgChains[nID] = newConfigurationChain(nID, recvs[nID], gov, cache,
			dbInst, &common.NullLogger{})
		recv.nodes[nID] = cfgChains[nID]
		recv.govs[nID] = gov
	}

	for _, cc := range cfgChains {
		cc.registerDKG(round, k)
	}

	for _, gov := range recv.govs {
		s.Require().Len(gov.DKGMasterPublicKeys(round), n)
	}

	errs := make(chan error, n)
	wg := sync.WaitGroup{}
	wg.Add(n)
	for _, cc := range cfgChains {
		go func(cc *configurationChain) {
			defer wg.Done()
			errs <- cc.runDKG(round)
		}(cc)
	}
	go func() {
		// Node 0 proposes NackComplaint to all others at 3λ but they should
		// be ignored because NackComplaint shoould be proposed before 2λ.
		time.Sleep(lambdaDKG * 3)
		nID := s.nIDs[0]
		for _, targetNode := range s.nIDs {
			if targetNode == nID {
				continue
			}
			recvs[nID].ProposeDKGComplaint(&typesDKG.Complaint{
				Round: round,
				PrivateShare: typesDKG.PrivateShare{
					ProposerID: targetNode,
					Round:      round,
				},
			})
		}
	}()
	wg.Wait()
	for range cfgChains {
		s.Require().NoError(<-errs)
	}
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round]; !exist {
			s.FailNow("Should be qualifyied")
		}
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			s.FailNow("Should be qualifyied")
		}
	}
}

func (s *ConfigurationChainTestSuite) TestMultipleTSig() {
	k := 2
	n := 7
	round := DKGDelayRound
	cfgChains := s.runDKG(k, n, round)

	hash1 := crypto.Keccak256Hash([]byte("Hash1"))
	hash2 := crypto.Keccak256Hash([]byte("Hash2"))

	psigs1 := s.preparePartialSignature(hash1, round, cfgChains)
	psigs2 := s.preparePartialSignature(hash2, round, cfgChains)

	tsigs1 := make([]crypto.Signature, 0, n)
	tsigs2 := make([]crypto.Signature, 0, n)

	errs := make(chan error, n*2)
	tsigChan1 := make(chan crypto.Signature, n)
	tsigChan2 := make(chan crypto.Signature, n)
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			continue
		}
		go func(cc *configurationChain) {
			tsig1, err := cc.runTSig(round, hash1)
			// Prevent racing by collecting errors and check in main thread.
			errs <- err
			tsigChan1 <- tsig1
		}(cc)
		go func(cc *configurationChain) {
			tsig2, err := cc.runTSig(round, hash2)
			// Prevent racing by collecting errors and check in main thread.
			errs <- err
			tsigChan2 <- tsig2
		}(cc)
		for _, psig := range psigs1 {
			err := cc.processPartialSignature(psig)
			s.Require().NoError(err)
		}
		for _, psig := range psigs2 {
			err := cc.processPartialSignature(psig)
			s.Require().NoError(err)
		}
	}
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			continue
		}
		s.Require().NoError(<-errs)
		tsig1 := <-tsigChan1
		for _, prevTsig := range tsigs1 {
			s.Equal(prevTsig, tsig1)
		}
		s.Require().NoError(<-errs)
		tsig2 := <-tsigChan2
		for _, prevTsig := range tsigs2 {
			s.Equal(prevTsig, tsig2)
		}
	}
}

func (s *ConfigurationChainTestSuite) TestTSigTimeout() {
	k := 2
	n := 7
	round := DKGDelayRound
	cfgChains := s.runDKG(k, n, round)
	timeout := 6 * time.Second

	hash := crypto.Keccak256Hash([]byte("🍯🍋"))

	psigs := s.preparePartialSignature(hash, round, cfgChains)

	errs := make(chan error, n)
	qualify := 0
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			continue
		}
		qualify++
		go func(cc *configurationChain) {
			_, err := cc.runTSig(round, hash)
			// Prevent racing by collecting errors and check in main thread.
			errs <- err
		}(cc)
		// Only 1 partial signature is provided.
		err := cc.processPartialSignature(psigs[0])
		s.Require().NoError(err)
	}
	time.Sleep(timeout)
	s.Require().Len(errs, qualify)
	for nID, cc := range cfgChains {
		if _, exist := cc.npks[round].QualifyNodeIDs[nID]; !exist {
			continue
		}
		s.Equal(<-errs, ErrNotEnoughtPartialSignatures)
	}
}

func (s *ConfigurationChainTestSuite) TestDKGSignerRecoverFromDB() {
	k := 2
	n := 7
	round := DKGDelayRound
	cfgChains := s.runDKG(k, n, round)
	hash := crypto.Keccak256Hash([]byte("Hash1"))
	// Make sure we have more than one configurationChain instance.
	s.Require().True(len(cfgChains) > 0)
	for _, cc := range cfgChains {
		psig1, err := cc.preparePartialSignature(round, hash)
		s.Require().NoError(err)
		// Create a cloned configurationChain, we should be able to recover
		// the DKG signer.
		clonedCC := newConfigurationChain(
			cc.ID, cc.recv, cc.gov, cc.cache, cc.db, cc.logger,
		)
		psig2, err := clonedCC.preparePartialSignature(round, hash)
		s.Require().NoError(err)
		// Make sure the signed signature are equal.
		s.Require().Equal(bytes.Compare(
			psig1.PartialSignature.Signature,
			psig2.PartialSignature.Signature), 0)
	}
}

func TestConfigurationChain(t *testing.T) {
	suite.Run(t, new(ConfigurationChainTestSuite))
}
