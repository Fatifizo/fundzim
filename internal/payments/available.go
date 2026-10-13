package payments

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/psp"
)

// Method availability reasons (GET /payment-methods/available).
const (
	AvailReasonExecutionDisabled = "EXECUTION_DISABLED" // no execution environment in this deployment
	AvailReasonNotAvailable      = "NOT_AVAILABLE"      // no confirmed, enabled, healthy provider route for this method and currency
)

// MethodAvailability is one method in GET /payment-methods/available.
type MethodAvailability struct {
	Method    string  `json:"method"`
	Category  string  `json:"category"`
	Available bool    `json:"available"`
	Simulated bool    `json:"simulated"`
	Reason    *string `json:"reason"`
}

// AvailabilityView is GET /payment-methods/available. It names no provider and exposes no capability evidence;
// test_mode is true whenever payments are not real (always, in Stage 9).
type AvailabilityView struct {
	Currency             string               `json:"currency"`
	ExecutionEnvironment string               `json:"execution_environment"`
	LivePayments         bool                 `json:"live_payments"`
	TestMode             bool                 `json:"test_mode"`
	Methods              []MethodAvailability `json:"methods"`
}

// availabilityOrder is the public order of method codes.
var availabilityOrder = []string{psp.MethodEcoCash, psp.MethodOneMoney, psp.MethodInnBucks, psp.MethodOMari, psp.MethodZimSwitch,
	psp.MethodCard, psp.MethodBankTransfer}

// AvailablePaymentMethods reports, for every known method, whether a donation in ccy could be paid with it now in
// this deployment. Availability comes from the router (capability evidence, provider enablement, allowlist,
// credentials, circuit and outages), never from configuration alone.
func (s *Service) AvailablePaymentMethods(ctx context.Context, ccy string) (AvailabilityView, error) {
	env := s.ExecEnv()
	v := AvailabilityView{Currency: ccy, ExecutionEnvironment: env, TestMode: env != psp.ExecProduction, Methods: []MethodAvailability{}}
	if env == psp.ExecProduction && s.PSP != nil {
		live, err := s.PSP.LiveExecution(ctx)
		if err != nil {
			return v, err
		}
		v.LivePayments = live
	}
	open := map[string]bool{}
	if env == psp.ExecLocalSimulator || env == psp.ExecProviderSandbox || v.LivePayments {
		opts, err := s.PSP.AvailableMethods(ctx, ccy, s.Simulated())
		if err != nil {
			return v, err
		}
		for _, o := range opts {
			open[o.Method] = true
		}
	}
	for _, m := range availabilityOrder {
		a := MethodAvailability{Method: m, Category: psp.MethodCategory(m), Available: open[m], Simulated: open[m] && s.Simulated()}
		if !a.Available {
			r := AvailReasonNotAvailable
			if env == psp.ExecDisabled {
				r = AvailReasonExecutionDisabled
			}
			a.Reason = &r
		}
		v.Methods = append(v.Methods, a)
	}
	return v, nil
}
