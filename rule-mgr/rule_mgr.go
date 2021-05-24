package rule_mgr

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/looplab/fsm"
	"github.com/meshplus/bitxhub-core/governance"
	g "github.com/meshplus/bitxhub-core/governance"
	"github.com/sirupsen/logrus"
)

const (
	RULEPREFIX = "rule-"
)

type RuleManager struct {
	g.Persister
}

type Rule struct {
	Address string             `json:"address"`
	ChainId string             `json:"chain_id"`
	Master  bool               `json:"master"`
	Status  g.GovernanceStatus `json:"status"`
	FSM     *fsm.FSM           `json:"fsm"`
}

func New(persister g.Persister) RuleMgr {
	return &RuleManager{persister}
}

func setFSM(rule *Rule) {
	rule.FSM = fsm.NewFSM(
		string(rule.Status),
		fsm.Events{
			// bind 1
			{Name: string(g.EventBind), Src: []string{string(g.GovernanceBindable), string(g.GovernanceFreezing), string(g.GovernanceLogouting)}, Dst: string(g.GovernanceBinding)},
			{Name: string(g.EventApprove), Src: []string{string(g.GovernanceBinding)}, Dst: string(g.GovernanceAvailable)},
			{Name: string(g.EventReject), Src: []string{string(g.GovernanceBinding)}, Dst: string(g.GovernanceBindable)},

			// unbind 1
			{Name: string(g.EventUnbind), Src: []string{string(g.GovernanceAvailable), string(g.GovernanceFreezing), string(g.GovernanceLogouting)}, Dst: string(g.GovernanceUnbinding)},
			{Name: string(g.EventApprove), Src: []string{string(g.GovernanceUnbinding)}, Dst: string(g.GovernanceBindable)},
			{Name: string(g.EventReject), Src: []string{string(g.GovernanceUnbinding)}, Dst: string(g.GovernanceAvailable)},

			// freeze 2
			{Name: string(g.EventFreeze), Src: []string{string(g.GovernanceAvailable), string(g.GovernanceBindable), string(g.GovernanceActivating), string(g.GovernanceBinding), string(g.GovernanceUnbinding), string(g.GovernanceLogouting)}, Dst: string(g.GovernanceFreezing)},
			{Name: string(g.EventApprove), Src: []string{string(g.GovernanceFreezing)}, Dst: string(g.GovernanceFrozen)},
			{Name: string(g.EventReject), Src: []string{string(g.GovernanceFreezing)}, Dst: string(g.GovernanceBindable)},

			// active 1
			{Name: string(g.EventActivate), Src: []string{string(g.GovernanceFrozen), string(g.GovernanceFreezing), string(g.GovernanceLogouting)}, Dst: string(g.GovernanceActivating)},
			{Name: string(g.EventApprove), Src: []string{string(g.GovernanceActivating)}, Dst: string(g.GovernanceBindable)},
			{Name: string(g.EventReject), Src: []string{string(g.GovernanceActivating)}, Dst: string(g.GovernanceFrozen)},

			// logout 3
			{Name: string(g.EventLogout), Src: []string{string(g.GovernanceAvailable), string(g.GovernanceBindable), string(g.GovernanceFrozen), string(g.GovernanceFreezing), string(g.GovernanceActivating), string(g.GovernanceBinding), string(g.GovernanceUnbinding)}, Dst: string(g.GovernanceLogouting)},
			{Name: string(g.EventApprove), Src: []string{string(g.GovernanceLogouting)}, Dst: string(g.GovernanceForbidden)},
			{Name: string(g.EventReject), Src: []string{string(g.GovernanceLogouting)}, Dst: string(g.GovernanceBindable)},
		},
		fsm.Callbacks{
			"enter_state": func(e *fsm.Event) { rule.Status = g.GovernanceStatus(rule.FSM.Current()) },
		},
	)
}

// Register record rule
func (rm *RuleManager) Register(chainId, ruleAddress string) (bool, []byte) {
	res := &g.RegisterResult{}
	res.ID = ruleAddress
	res.IsRegistered = false

	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(chainId), &rules); ok {
		for _, r := range rules {
			if ruleAddress == r.Address {
				rm.Persister.Logger().WithFields(logrus.Fields{
					"id":   chainId,
					"addr": ruleAddress,
				}).Info("Rule has deployed")
				res.IsRegistered = true
				break
			}
		}
	}

	if !res.IsRegistered {
		rules = append(rules, &Rule{ruleAddress, chainId, false, g.GovernanceBindable, nil})
		rm.SetObject(rm.ruleKey(chainId), rules)
	}

	resData, err := json.Marshal(res)
	if err != nil {
		return false, []byte(err.Error())
	}

	return true, resData
}

// BindPre checks if the rule address can bind with appchain id and record rule. (only check, not modify infomation)
// force will be true when update master rule
func (rm *RuleManager) BindPre(chainId, ruleAddress string, force bool) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(chainId), &rules); !ok {
		return false, []byte("this appchain's rules do not exist")
	}

	isExisted := false
	for _, r := range rules {
		if ruleAddress == r.Address {
			if r.Status != g.GovernanceBindable {
				return false, []byte("The rule is in an unbindable state: " + r.Status)
			} else {
				isExisted = true
			}
		} else {
			if g.GovernanceAvailable == r.Status && !force {
				return false, []byte("There is already a bound (available) validation rule. Please unbind the rule before binding other validation rules")
			}
			if true == r.Master && g.GovernanceAvailable != r.Status {
				return false, []byte(fmt.Sprintf("The master rule is changing(%s) now. Please wait until the proposal close before binding new rule", r.Status))
			}
		}
	}

	if !isExisted {
		return false, []byte("the rule does not exist ")
	}

	return true, nil
}

func (rm *RuleManager) SetMaster(ruleAddress string, chainId []byte, master bool) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(string(chainId)), &rules); !ok {
		return false, []byte("this appchain's rules do not exist")
	}

	flag := false
	for _, r := range rules {
		if ruleAddress == r.Address {
			flag = true
			r.Master = master
		}
	}

	if !flag {
		return false, []byte("the rule does not exist ")
	}

	rm.SetObject(rm.ruleKey(string(chainId)), rules)

	return true, nil
}

func (rm *RuleManager) ChangeStatus(ruleAddress, trigger string, chainId []byte) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(string(chainId)), &rules); !ok {
		return false, []byte("this appchain's rules do not exist")
	}

	flag := false
	for _, r := range rules {
		if ruleAddress == r.Address {
			flag = true
			setFSM(r)
			err := r.FSM.Event(trigger)
			if err != nil {
				return false, []byte(fmt.Sprintf("change status error: %v", err))
			}
		}
	}

	if !flag {
		return false, []byte("the rule does not exist ")
	}

	rm.SetObject(rm.ruleKey(string(chainId)), rules)

	return true, nil
}

// CountAvailable counts all rules of one appchain including available
func (rm *RuleManager) CountAvailable(chainId []byte) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(string(chainId)), &rules); !ok {
		return true, []byte(strconv.Itoa(0))
	}

	count := 0
	for _, r := range rules {
		if g.GovernanceAvailable == r.Status {
			count++
		}
	}

	return true, []byte(strconv.Itoa(count))
}

func (rm *RuleManager) CountAll(chainId []byte) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(string(chainId)), &rules); !ok {
		return true, []byte(strconv.Itoa(0))
	}

	return true, []byte(strconv.Itoa(len(rules)))
}

// Appchains returns all appchains
func (rm *RuleManager) All(chainId []byte) (bool, []byte) {
	ok, data := rm.Get(rm.ruleKey(string(chainId)))
	if !ok {
		return true, nil
	}

	return true, data
}

func (rm *RuleManager) QueryById(ruleAddress string, chainId []byte) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(string(chainId)), &rules); !ok {
		return false, []byte(fmt.Errorf("this appchain's rules do not exist").Error())
	}

	for _, r := range rules {
		if ruleAddress == r.Address {
			ruleData, err := json.Marshal(r)
			if err != nil {
				return false, []byte(fmt.Sprintf("marshal rule error: %v", err))
			}
			return true, ruleData
		}
	}

	return false, []byte(fmt.Errorf("this rule does not exist").Error())
}

func (rm *RuleManager) GetAvailableRuleAddress(chainId string) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(chainId), &rules); !ok {
		return false, []byte("this appchain's rules do not exist")
	}

	for _, r := range rules {
		if g.GovernanceAvailable == r.Status {
			return true, []byte(r.Address)
		}
	}

	return false, []byte("this appchain's available rule does not exist")
}

func (rm *RuleManager) GetMaster(chainId string) (bool, []byte) {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(chainId), &rules); !ok {
		return false, []byte("this appchain's rules do not exist")
	}

	for _, r := range rules {
		if true == r.Master {
			ruleData, err := json.Marshal(r)
			if err != nil {
				return false, []byte(fmt.Sprintf("marshal rule error: %v", err))
			}
			return true, ruleData
		}
	}

	return false, []byte("this appchain's master rule does not exist")
}

func (rm *RuleManager) HasMaster(chainId string) bool {
	rules := make([]*Rule, 0)
	if ok := rm.GetObject(rm.ruleKey(chainId), &rules); !ok {
		return false
	}

	for _, r := range rules {
		if true == r.Master {
			return true
		}
	}

	return false
}

func (rm *RuleManager) IsAvailable(chainId, ruleAddress string) (bool, []byte) {
	is, data := rm.QueryById(ruleAddress, []byte(chainId))

	if !is {
		return false, []byte("get rule info error: " + string(data))
	}

	rule := &Rule{}
	if err := json.Unmarshal(data, rule); err != nil {
		return false, []byte("unmarshal rule error: " + err.Error())
	}

	if rule.Status != governance.GovernanceAvailable {
		return false, []byte("the rule status is " + string(rule.Status))
	}

	return true, nil
}

func (rm *RuleManager) ruleKey(id string) string {
	return RULEPREFIX + id
}
