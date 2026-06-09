package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgApplicationRepository struct {
	pool *pgxpool.Pool
}

func NewApplicationRepository(pool *pgxpool.Pool) *pgApplicationRepository {
	return &pgApplicationRepository{pool: pool}
}

// Save persiste la aplicación completa (primera vez).
func (r *pgApplicationRepository) Save(ctx context.Context, app *domain.OnboardingApplication) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	info := app.BusinessInfo()

	_, err := conn.Exec(ctx, `
		INSERT INTO onboarding_applications
			(id, tenant_id, status, legal_name, tax_id, business_category,
			 addr_street, addr_city, addr_state, addr_country, addr_postal,
			 website, phone_number, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		app.ID().String(), app.TenantID().String(), app.Status().String(),
		info.LegalName, info.TaxID.String(), info.BusinessCategory.String(),
		info.Address.Street, info.Address.City, info.Address.State,
		info.Address.Country, info.Address.PostalCode,
		info.Website, info.PhoneNumber,
		app.CreatedAt(), app.UpdatedAt(),
	)
	return wrapErr("save", err)
}

// Update persiste los cambios del aggregate (status + colecciones).
func (r *pgApplicationRepository) Update(ctx context.Context, app *domain.OnboardingApplication) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	_, err := conn.Exec(ctx, `
		UPDATE onboarding_applications SET
			status=$2, review_notes=$3, rejection_reason=$4,
			submitted_at=$5, reviewed_at=$6, updated_at=$7
		WHERE id=$1`,
		app.ID().String(), app.Status().String(),
		nullStr(app.ReviewNotes()), nullStr(app.RejectionReason()),
		app.SubmittedAt(), app.ReviewedAt(), app.UpdatedAt(),
	)
	if err != nil {
		return wrapErr("update status", err)
	}

	// Insertar documentos nuevos (append-only: ON CONFLICT DO NOTHING).
	for _, doc := range app.Documents() {
		if _, err := conn.Exec(ctx, `
			INSERT INTO onboarding_documents
				(id, application_id, tenant_id, document_type, reference, status, uploaded_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (id) DO NOTHING`,
			doc.ID().String(), app.ID().String(), app.TenantID().String(),
			doc.DocumentType().String(), doc.Reference(), doc.Status(),
			doc.UploadedAt(),
		); err != nil {
			return wrapErr("upsert document", err)
		}
	}

	// Insertar personas nuevas (append-only).
	for _, p := range app.Persons() {
		if _, err := conn.Exec(ctx, `
			INSERT INTO onboarding_persons
				(id, application_id, tenant_id, full_name, role,
				 identity_doc_type, identity_doc_number, nationality, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (id) DO NOTHING`,
			p.ID().String(), app.ID().String(), app.TenantID().String(),
			p.FullName(), p.Role().String(),
			nullStr(p.IdentityDocType()), nullStr(p.IdentityDocNumber()),
			nullStr(p.Nationality()), p.CreatedAt(),
		); err != nil {
			return wrapErr("upsert person", err)
		}
	}

	// Upsert de cuenta bancaria.
	if ba := app.BankAccount(); ba != nil {
		if _, err := conn.Exec(ctx, `
			INSERT INTO onboarding_bank_accounts
				(id, application_id, tenant_id, account_type, account_number,
				 bank_name, holder_name, currency, verified, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (application_id) DO UPDATE
				SET account_type=$4, account_number=$5, bank_name=$6,
				    holder_name=$7, currency=$8`,
			ba.ID().String(), app.ID().String(), app.TenantID().String(),
			ba.AccountType().String(), ba.AccountNumber(),
			nullStr(ba.BankName()), ba.HolderName(), ba.Currency(),
			ba.Verified(), ba.CreatedAt(),
		); err != nil {
			return wrapErr("upsert bank account", err)
		}
	}

	return nil
}

func (r *pgApplicationRepository) FindByID(ctx context.Context, id domain.ApplicationID) (*domain.OnboardingApplication, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseQuery+" WHERE a.id = $1", id.String())
	return r.scanWithRelations(ctx, conn, row)
}

func (r *pgApplicationRepository) FindByTenantID(ctx context.Context, tenantID domain.TenantID) (*domain.OnboardingApplication, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseQuery+" WHERE a.tenant_id = $1", tenantID.String())
	return r.scanWithRelations(ctx, conn, row)
}

const baseQuery = `
	SELECT a.id, a.tenant_id, a.status, a.legal_name, a.tax_id, a.business_category,
	       a.addr_street, a.addr_city, a.addr_state, a.addr_country, a.addr_postal,
	       a.website, a.phone_number,
	       a.review_notes, a.rejection_reason,
	       a.submitted_at, a.reviewed_at, a.created_at, a.updated_at
	FROM onboarding_applications a`

func (r *pgApplicationRepository) scanWithRelations(ctx context.Context, conn postgres.Conn, row pgx.Row) (*domain.OnboardingApplication, error) {
	var (
		idStr, tenantIDStr, status, legalName, taxIDStr, catStr string
		street, city, state, country, postal                    string
		website, phone                                          string
		reviewNotes, rejectionReason                            *string
		submittedAt, reviewedAt                                 *time.Time
		createdAt, updatedAt                                    time.Time
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &status, &legalName, &taxIDStr, &catStr,
		&street, &city, &state, &country, &postal,
		&website, &phone,
		&reviewNotes, &rejectionReason,
		&submittedAt, &reviewedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrApplicationNotFound
		}
		return nil, fmt.Errorf("application repo: scan: %w", err)
	}

	taxID, _ := domain.ParseTaxID(taxIDStr)
	cat, _ := domain.ParseBusinessCategory(catStr)

	info := domain.BusinessInfo{
		LegalName:        legalName,
		TaxID:            taxID,
		BusinessCategory: cat,
		Address:          domain.Address{Street: street, City: city, State: state, Country: country, PostalCode: postal},
		Website:          website,
		PhoneNumber:      phone,
	}

	// Cargar documentos
	docRows, err := conn.Query(ctx, `
		SELECT id, document_type, reference, status, COALESCE(notes,''), uploaded_at
		FROM onboarding_documents WHERE application_id=$1`, idStr)
	if err != nil {
		return nil, fmt.Errorf("application repo: load documents: %w", err)
	}
	defer docRows.Close()

	var docs []domain.BusinessDocument
	for docRows.Next() {
		var did, dtype, ref, dstatus, notes string
		var uploadedAt time.Time
		docRows.Scan(&did, &dtype, &ref, &dstatus, &notes, &uploadedAt)
		dt, _ := domain.ParseDocumentType(dtype)
		docs = append(docs, domain.ReconstituteDocument(
			domain.DocumentID(did), dt, ref, notes,
			domain.DocumentStatus(dstatus), uploadedAt.UTC(),
		))
	}

	// Cargar personas
	personRows, err := conn.Query(ctx, `
		SELECT id, full_name, role, COALESCE(identity_doc_type,''),
		       COALESCE(identity_doc_number,''), COALESCE(nationality,''), created_at
		FROM onboarding_persons WHERE application_id=$1`, idStr)
	if err != nil {
		return nil, fmt.Errorf("application repo: load persons: %w", err)
	}
	defer personRows.Close()

	var persons []domain.Person
	for personRows.Next() {
		var pid, fullName, roleStr, idocType, idocNum, nationality string
		var pc time.Time
		personRows.Scan(&pid, &fullName, &roleStr, &idocType, &idocNum, &nationality, &pc)
		role, _ := domain.ParsePersonRole(roleStr)
		persons = append(persons, domain.ReconstitutePerson(
			domain.PersonID(pid), fullName, role, idocType, idocNum, nationality, pc.UTC(),
		))
	}

	// Cargar cuenta bancaria (si existe)
	var bankAccount *domain.BankAccount
	baRow := conn.QueryRow(ctx, `
		SELECT id, account_type, account_number, COALESCE(bank_name,''),
		       holder_name, currency, verified, created_at
		FROM onboarding_bank_accounts WHERE application_id=$1`, idStr)
	var baid, batype, banum, babank, baholder, bacur string
	var baverified bool
	var bac time.Time
	if scanErr := baRow.Scan(&baid, &batype, &banum, &babank, &baholder, &bacur, &baverified, &bac); scanErr == nil {
		bt, _ := domain.ParseBankAccountType(batype)
		ba := domain.ReconstituteBankAccount(
			domain.BankAccountID(baid), bt, banum, babank, baholder, bacur, baverified, bac.UTC(),
		)
		bankAccount = &ba
	}

	app := domain.ReconstituteOnboardingApplication(
		domain.ApplicationID(idStr),
		domain.TenantID(tenantIDStr),
		domain.ApplicationStatus(status),
		info,
		bankAccount, docs, persons,
		deref(reviewNotes), deref(rejectionReason),
		submittedAt, reviewedAt,
		createdAt.UTC(), updatedAt.UTC(),
	)
	return app, nil
}

func wrapErr(op string, err error) error {
	if err != nil {
		return fmt.Errorf("application repo: %s: %w", op, err)
	}
	return nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
