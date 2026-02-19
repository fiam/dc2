package testprofile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	Version1 = 1
)

type Hook string

const (
	HookBefore Hook = "before"
	HookAfter  Hook = "after"
)

type Phase string

const (
	PhaseAllocate Phase = "allocate"
	PhaseStart    Phase = "start"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == 0 {
		return nil
	}
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("decoding duration: %w", err)
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parsing duration %q: %w", raw, err)
	}
	d.Duration = parsed
	return nil
}

type IntRange struct {
	GTE *int `yaml:"gte"`
	LTE *int `yaml:"lte"`
	GT  *int `yaml:"gt"`
	LT  *int `yaml:"lt"`
}

type StringMatcher struct {
	Equals *string `yaml:"equals"`
	Glob   *string `yaml:"glob"`
}

type InstanceFilters struct {
	Type      *StringMatcher `yaml:"type"`
	VCPU      *IntRange      `yaml:"vcpu"`
	MemoryMiB *IntRange      `yaml:"memory_mib"`
}

type MarketFilters struct {
	Type *string `yaml:"type"`
}

type RequestFilters struct {
	Market *MarketFilters `yaml:"market"`
}

type RuleWhen struct {
	Action   string           `yaml:"action"`
	Request  *RequestFilters  `yaml:"request"`
	Instance *InstanceFilters `yaml:"instance"`
}

type DelayHooks struct {
	Allocate *Duration `yaml:"allocate"`
	Start    *Duration `yaml:"start"`
}

type DelaySpec struct {
	Before DelayHooks `yaml:"before"`
	After  DelayHooks `yaml:"after"`
}

type SpotReclaimSpec struct {
	After  *Duration `yaml:"after"`
	Notice *Duration `yaml:"notice"`
}

type SpotReclaimConfig struct {
	After  *time.Duration
	Notice *time.Duration
}

type Rule struct {
	Name        string          `yaml:"name"`
	When        RuleWhen        `yaml:"when"`
	Delay       DelaySpec       `yaml:"delay"`
	SpotReclaim SpotReclaimSpec `yaml:"reclaim"`
}

type Profile struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

type MatchInput struct {
	Action       string
	MarketType   string
	InstanceType string
	VCPU         int
	MemoryMiB    int
}

func LoadFile(path string) (*Profile, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil, fmt.Errorf("test profile path is empty")
	}
	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return nil, fmt.Errorf("reading test profile %q: %w", path, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)

	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return nil, fmt.Errorf("decoding test profile %q: %w", path, err)
	}
	if err := profile.validate(); err != nil {
		return nil, fmt.Errorf("validating test profile %q: %w", path, err)
	}
	return &profile, nil
}

func (p *Profile) validate() error {
	if p.Version != Version1 {
		return fmt.Errorf("unsupported version %d", p.Version)
	}
	for i := range p.Rules {
		if err := p.Rules[i].validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	return nil
}

func (r *Rule) validate() error {
	if r.When.Instance != nil && r.When.Instance.Type != nil {
		matcher := r.When.Instance.Type
		if matcher.Equals != nil && matcher.Glob != nil {
			return fmt.Errorf("instance.type cannot define both equals and glob")
		}
	}
	if r.SpotReclaim.After != nil && r.SpotReclaim.After.Duration < 0 {
		return fmt.Errorf("reclaim.after must be >= 0")
	}
	if r.SpotReclaim.Notice != nil && r.SpotReclaim.Notice.Duration < 0 {
		return fmt.Errorf("reclaim.notice must be >= 0")
	}
	return nil
}

func (p *Profile) Delay(hook Hook, phase Phase, in MatchInput) time.Duration {
	if p == nil {
		return 0
	}
	total := time.Duration(0)
	for i := range p.Rules {
		rule := p.Rules[i]
		if !rule.matches(in) {
			continue
		}
		total += rule.delayFor(hook, phase)
	}
	return total
}

func (r Rule) matches(in MatchInput) bool {
	if action := strings.TrimSpace(r.When.Action); action != "" && !strings.EqualFold(action, in.Action) {
		return false
	}
	if !matchRequestFilters(r.When.Request, in) {
		return false
	}
	if !matchInstanceFilters(r.When.Instance, in) {
		return false
	}
	return true
}

func matchRequestFilters(filters *RequestFilters, in MatchInput) bool {
	if filters == nil || filters.Market == nil || filters.Market.Type == nil {
		return true
	}
	expected := strings.TrimSpace(*filters.Market.Type)
	if expected == "" {
		return true
	}
	marketType := strings.TrimSpace(in.MarketType)
	if marketType == "" {
		marketType = "on-demand"
	}
	return strings.EqualFold(expected, marketType)
}

func matchInstanceFilters(filters *InstanceFilters, in MatchInput) bool {
	if filters == nil {
		return true
	}
	if !matchInstanceType(filters.Type, in.InstanceType) {
		return false
	}
	if !matchIntRange(filters.VCPU, in.VCPU) {
		return false
	}
	if !matchIntRange(filters.MemoryMiB, in.MemoryMiB) {
		return false
	}
	return true
}

func matchInstanceType(matcher *StringMatcher, value string) bool {
	if matcher == nil {
		return true
	}
	if matcher.Equals != nil {
		return strings.EqualFold(strings.TrimSpace(*matcher.Equals), strings.TrimSpace(value))
	}
	if matcher.Glob != nil {
		pattern := strings.TrimSpace(*matcher.Glob)
		if pattern == "" {
			return true
		}
		ok, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(strings.TrimSpace(value)))
		return err == nil && ok
	}
	return true
}

func matchIntRange(r *IntRange, value int) bool {
	if r == nil {
		return true
	}
	if r.GTE != nil && value < *r.GTE {
		return false
	}
	if r.LTE != nil && value > *r.LTE {
		return false
	}
	if r.GT != nil && value <= *r.GT {
		return false
	}
	if r.LT != nil && value >= *r.LT {
		return false
	}
	return true
}

func (r Rule) delayFor(hook Hook, phase Phase) time.Duration {
	var hooks DelayHooks
	switch hook {
	case HookBefore:
		hooks = r.Delay.Before
	case HookAfter:
		hooks = r.Delay.After
	default:
		return 0
	}

	var duration *Duration
	switch phase {
	case PhaseAllocate:
		duration = hooks.Allocate
	case PhaseStart:
		duration = hooks.Start
	default:
		return 0
	}
	if duration == nil {
		return 0
	}
	return duration.Duration
}

func (p *Profile) SpotReclaim(in MatchInput) SpotReclaimConfig {
	if p == nil {
		return SpotReclaimConfig{}
	}

	var out SpotReclaimConfig
	for i := range p.Rules {
		rule := p.Rules[i]
		if !rule.matches(in) {
			continue
		}
		if rule.SpotReclaim.After != nil {
			after := rule.SpotReclaim.After.Duration
			out.After = &after
		}
		if rule.SpotReclaim.Notice != nil {
			notice := rule.SpotReclaim.Notice.Duration
			out.Notice = &notice
		}
	}
	return out
}
