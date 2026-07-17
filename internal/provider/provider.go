// Package provider is the core-side host for tds language providers: discovery,
// subprocess launch, the capabilities handshake (with version negotiation), and
// per-request timeouts — all with failure isolation so a missing, incompatible,
// or crashing provider degrades gracefully instead of taking down the core.
// See docs/protocol.md and design §9.
package provider

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

// DefaultTimeout bounds the capabilities handshake and each request.
const DefaultTimeout = 30 * time.Second

// Spec describes how to launch one provider.
type Spec struct {
	Name    string   // logical name, e.g. "ruby"
	Command []string // argv; Command[0] is the executable
	Env     []string // extra environment (KEY=VALUE), appended to os.Environ()
}

// Provider is a launched, resident provider process the core talks to over the
// JSONL protocol. Calls are serialized; the process may be reused across many
// requests to amortize startup.
type Provider struct {
	Spec Spec
	Caps protocol.Capabilities

	cmd   *exec.Cmd
	stdin io.WriteCloser
	enc   *protocol.Encoder
	dec   *protocol.Decoder

	mu      sync.Mutex
	nextID  int
	dead    bool
	timeout time.Duration // default per-request budget when the ctx has no deadline
}

// LaunchOptions tune a single launch.
type LaunchOptions struct {
	Timeout time.Duration // handshake + per-request budget; DefaultTimeout if zero
	Stderr  io.Writer     // provider stderr (logs); os.Stderr if nil
}

func (o LaunchOptions) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

// Launch starts the provider and performs the capabilities handshake, rejecting
// a provider whose protocol major the core doesn't speak.
func Launch(ctx context.Context, spec Spec, opts LaunchOptions) (*Provider, error) {
	if len(spec.Command) == 0 {
		return nil, fmt.Errorf("provider %q: empty command", spec.Name)
	}
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("provider %q: start: %w", spec.Name, err)
	}

	p := &Provider{
		Spec:    spec,
		cmd:     cmd,
		stdin:   stdin,
		enc:     protocol.NewEncoder(stdin),
		dec:     protocol.NewDecoder(stdout),
		timeout: opts.timeout(),
	}

	hctx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()
	var caps protocol.Capabilities
	if err := p.call(hctx, protocol.OpCapabilities,
		protocol.CapabilitiesParams{CoreProtocols: protocol.SupportedMajors}, &caps); err != nil {
		p.kill()
		return nil, fmt.Errorf("provider %q: capabilities handshake: %w", spec.Name, err)
	}
	if !protocol.Compatible(caps.Protocol) {
		p.kill()
		return nil, fmt.Errorf("provider %q: incompatible protocol %q (core speaks %v)",
			spec.Name, caps.Protocol, protocol.SupportedMajors)
	}
	p.Caps = caps
	return p, nil
}

// call performs one request/response round trip with a deadline. On timeout the
// provider is killed (a wedged provider is treated as dead — failure isolation).
func (p *Provider) call(ctx context.Context, op string, params, result any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return fmt.Errorf("provider %q is dead", p.Spec.Name)
	}

	// Enforce a default per-request timeout unless the caller set a deadline.
	if _, ok := ctx.Deadline(); !ok && p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	p.nextID++
	id := p.nextID
	req, err := protocol.NewRequest(id, op, params)
	if err != nil {
		return err
	}
	if err := p.enc.Encode(req); err != nil {
		p.markDead()
		return fmt.Errorf("send %s: %w", op, err)
	}

	type outcome struct {
		resp *protocol.Response
		err  error
	}
	ch := make(chan outcome, 1)
	go func() {
		resp, err := p.dec.DecodeResponse()
		ch <- outcome{resp, err}
	}()

	select {
	case <-ctx.Done():
		p.markDead()
		return fmt.Errorf("%s timed out: %w", op, ctx.Err())
	case o := <-ch:
		if o.err != nil {
			p.markDead()
			return fmt.Errorf("read %s response: %w", op, o.err)
		}
		if o.resp.ID != id {
			p.markDead()
			return fmt.Errorf("%s: response id %d != request id %d", op, o.resp.ID, id)
		}
		if !o.resp.OK {
			if o.resp.Error != nil {
				return o.resp.Error // operation-level error; provider stays alive
			}
			return fmt.Errorf("%s: not ok with no error", op)
		}
		if result != nil {
			return o.resp.ResolveResult(result)
		}
		return nil
	}
}

// Structure requests the structural index for a batch of files.
func (p *Provider) Structure(ctx context.Context, params protocol.StructureParams) (protocol.StructureResult, error) {
	var out protocol.StructureResult
	err := p.call(ctx, protocol.OpStructure, params, &out)
	return out, err
}

// Analyze requests analyzer findings for a batch of files.
func (p *Provider) Analyze(ctx context.Context, params protocol.AnalyzeParams) (protocol.AnalyzeResult, error) {
	var out protocol.AnalyzeResult
	err := p.call(ctx, protocol.OpAnalyze, params, &out)
	return out, err
}

// Close shuts the provider down: close stdin (EOF) and wait briefly, then kill.
func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return nil
	}
	_ = p.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-time.After(2 * time.Second):
		p.markDead()
		return nil
	case err := <-done:
		p.dead = true
		return err
	}
}

// markDead kills the process; caller holds p.mu (or is Close).
func (p *Provider) markDead() {
	if p.dead {
		return
	}
	p.dead = true
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	go p.cmd.Wait() // reap
}

func (p *Provider) kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markDead()
}
