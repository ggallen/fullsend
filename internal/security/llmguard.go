package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LLMGuardResult holds the output from the prompt injection scanner.
type LLMGuardResult struct {
	IsInjection bool    `json:"is_injection"`
	RiskScore   float64 `json:"risk_score"`
	Detail      string  `json:"detail"`
}

// LLMGuardScanner runs the ProtectAI DeBERTa-v3 ONNX model for ML-based
// prompt injection detection. Loads the model directly via onnxruntime
// (no torch, no llm-guard wrapper). Runs in Path A (GHA workflow pre-step)
// and Path B (sandbox pre-agent scan).
//
// When Required is true (e.g. inside the sandbox where Python + the model
// are baked into the image), a missing Python binary is treated as tampering
// and the scanner fails closed. When Required is false (e.g. Path A GHA
// pre-step), a missing runtime is logged as a warning and the scanner fails
// open.
type LLMGuardScanner struct {
	Threshold float64
	MatchType string // "sentence" or "full"
	Required  bool   // fail closed when Python/onnxruntime unavailable
}

// NewLLMGuardScanner creates a scanner with the given threshold, match type,
// and required flag. When required is true the scanner fails closed if Python
// or onnxruntime is unavailable (intended for the sandbox where both are
// baked into the image).
func NewLLMGuardScanner(threshold float64, matchType string, required bool) *LLMGuardScanner {
	if threshold == 0 {
		threshold = 0.92
	}
	if matchType == "" {
		matchType = "sentence"
	}
	return &LLMGuardScanner{
		Threshold: threshold,
		MatchType: matchType,
		Required:  required,
	}
}

// Scan runs the ProtectAI DeBERTa-v3 prompt injection scanner on the
// given text. When Required is false, fails open if Python is unavailable.
// When Required is true, fails closed (missing Python treated as tampering).
func (s *LLMGuardScanner) Scan(text string) ScanResult {
	script := fmt.Sprintf(`
import json, sys
try:
    import numpy as np
    import onnxruntime as ort
    from transformers import AutoTokenizer
    from huggingface_hub import hf_hub_download
    model_name = "protectai/deberta-v3-base-prompt-injection-v2"
    tokenizer = AutoTokenizer.from_pretrained(model_name, subfolder="onnx")
    model_path = hf_hub_download(model_name, "onnx/model.onnx")
    session = ort.InferenceSession(model_path)
    input_names = [i.name for i in session.get_inputs()]
    def score_text(t):
        inputs = tokenizer(t, return_tensors="np", truncation=True, max_length=512)
        ort_inputs = {k: v for k, v in inputs.items() if k in input_names}
        logits = session.run(None, ort_inputs)[0][0]
        e = np.exp(logits - np.max(logits))
        probs = e / e.sum()
        return float(probs[1])
    text = sys.stdin.read()
    match_type = %q
    threshold = %f
    if match_type == "sentence":
        from nltk.tokenize import sent_tokenize
        sentences = sent_tokenize(text.strip())
        risk_score = max((score_text(s) for s in sentences), default=0.0)
    else:
        risk_score = score_text(text)
    is_injection = risk_score >= threshold
    json.dump({"is_injection": is_injection, "risk_score": round(risk_score, 4), "detail": "DeBERTa-v3 ONNX ML scan"}, sys.stdout)
except ImportError:
    import os
    if os.environ.get("LLM_GUARD_REQUIRED", "") == "1":
        json.dump({"is_injection": True, "risk_score": 1.0, "detail": "onnxruntime not installed but LLM_GUARD_REQUIRED=1"}, sys.stdout)
        sys.exit(1)
    json.dump({"is_injection": False, "risk_score": 0, "detail": "onnxruntime not installed"}, sys.stdout)
except Exception as e:
    json.dump({"is_injection": True, "risk_score": 1.0, "detail": "scanner error (fail-closed): " + str(e)}, sys.stdout)
`, s.MatchType, s.Threshold)

	cmd := exec.Command("python3", "-c", script)
	cmd.Stdin = strings.NewReader(text)
	if s.Required {
		cmd.Env = append(os.Environ(), "LLM_GUARD_REQUIRED=1")
	}

	output, err := cmd.Output()
	if err != nil {
		// Fail open only when python3 is not installed.
		// Script errors (non-zero exit) may still have JSON output — continue parsing.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			if s.Required {
				return ScanResult{
					Safe: false,
					Findings: []Finding{{
						Scanner:  "llm_guard",
						Name:     "python_unavailable",
						Severity: "critical",
						Detail:   "Python not available but prompt injection scanner is required (possible tampering)",
						Position: -1,
					}},
				}
			}
			// Python not available — fail open with logged warning.
			fmt.Fprintln(os.Stderr, "WARN: prompt injection scanner skipped — python3 not available")
			return ScanResult{Safe: true}
		}
	}

	var result LLMGuardResult
	if err := json.Unmarshal(output, &result); err != nil {
		// Cannot parse scanner output — fail closed.
		return ScanResult{
			Safe: false,
			Findings: []Finding{{
				Scanner:  "llm_guard",
				Name:     "scanner_error",
				Severity: "high",
				Detail:   "prompt injection scanner returned unparseable output (fail-closed)",
				Position: -1,
			}},
		}
	}

	if result.IsInjection {
		return ScanResult{
			Safe: false,
			Findings: []Finding{
				{
					Scanner:  "llm_guard",
					Name:     "prompt_injection_ml",
					Severity: "critical",
					Detail:   fmt.Sprintf("DeBERTa-v3 detected injection (risk_score=%.3f, threshold=%.3f)", result.RiskScore, s.Threshold),
					Position: -1,
				},
			},
		}
	}

	return ScanResult{Safe: true}
}

// Name returns the scanner identifier.
func (s *LLMGuardScanner) Name() string {
	return "llm_guard"
}
