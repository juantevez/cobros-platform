package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

const (
	secretLength = 32 // bytes de entropía para el secreto de la API key
	prefixLength = 8  // chars del prefix visible
)

// IssueApiKeyUseCase genera una nueva API key para un tenant.
//
// La key completa solo se retorna una vez en IssueApiKeyResult.FullKey.
// Lo que se persiste es el prefix (para buscar) y el hash del secreto
// (para verificar). Si el usuario pierde la key, debe generar una nueva.
//
// Formato de la key:  <env>_<prefix>_<secret>
// Ejemplo:            test_Xk3mPQrS_7fGhJ9kLpQrStUvWxYzAb...
type IssueApiKeyUseCase struct {
	tenantRepo TenantRepository
	apiKeyRepo ApiKeyRepository
	hasher     PasswordHasher
	txManager  TxManager
	publisher  EventPublisher
}

func NewIssueApiKeyUseCase(
	tenantRepo TenantRepository,
	apiKeyRepo ApiKeyRepository,
	hasher PasswordHasher,
	txManager TxManager,
	publisher EventPublisher,
) *IssueApiKeyUseCase {
	return &IssueApiKeyUseCase{
		tenantRepo: tenantRepo,
		apiKeyRepo: apiKeyRepo,
		hasher:     hasher,
		txManager:  txManager,
		publisher:  publisher,
	}
}

func (uc *IssueApiKeyUseCase) Execute(ctx context.Context, cmd IssueApiKeyCmd) (IssueApiKeyResult, error) {
	// ── 1. Validar inputs ────────────────────────────────────────────────────

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return IssueApiKeyResult{}, err
	}

	env, err := domain.ParseEnvironment(cmd.Environment)
	if err != nil {
		return IssueApiKeyResult{}, err
	}

	if cmd.Name == "" {
		return IssueApiKeyResult{}, fmt.Errorf("api key name is required")
	}

	scopes := make([]domain.Scope, 0, len(cmd.Scopes))
	for _, s := range cmd.Scopes {
		sc, err := domain.ParseScope(s)
		if err != nil {
			return IssueApiKeyResult{}, err
		}
		scopes = append(scopes, sc)
	}
	if len(scopes) == 0 {
		return IssueApiKeyResult{}, fmt.Errorf("at least one scope is required")
	}

	// ── 2. Verificar tenant ──────────────────────────────────────────────────

	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return IssueApiKeyResult{}, fmt.Errorf("find tenant: %w", err)
	}
	if !tenant.IsActive() {
		return IssueApiKeyResult{}, domain.ErrTenantNotActive
	}
	// Una key de producción solo puede crearse en un tenant de producción.
	if env == domain.EnvironmentProduction && !tenant.CanProcessRealPayments() {
		return IssueApiKeyResult{}, fmt.Errorf("tenant is not approved for production")
	}

	// ── 3. Generar secreto y prefix ──────────────────────────────────────────
	// Fuera de la tx porque puede ser lento.

	secret, err := generateSecret(secretLength)
	if err != nil {
		return IssueApiKeyResult{}, fmt.Errorf("generate secret: %w", err)
	}
	prefix := secret[:prefixLength]

	keyHash, err := uc.hasher.Hash(secret)
	if err != nil {
		return IssueApiKeyResult{}, fmt.Errorf("hash secret: %w", err)
	}

	// La key completa que se retorna al cliente.
	fullKey := fmt.Sprintf("%s_%s_%s", env.String(), prefix, secret)

	// ── 4. Construir el agregado ─────────────────────────────────────────────

	id := domain.NewApiKeyID()
	apiKey, err := domain.NewApiKey(id, tenantID, cmd.Name, prefix, keyHash, env, scopes)
	if err != nil {
		return IssueApiKeyResult{}, err
	}

	// ── 5. Persistir y publicar ──────────────────────────────────────────────

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.apiKeyRepo.Save(ctx, apiKey); err != nil {
			return fmt.Errorf("save api key: %w", err)
		}
		return uc.publisher.Publish(ctx, apiKey.PullEvents()...)
	}); err != nil {
		return IssueApiKeyResult{}, err
	}

	return IssueApiKeyResult{
		ApiKeyID: id.String(),
		FullKey:  fullKey,
		Prefix:   prefix,
	}, nil
}
