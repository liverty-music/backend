package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// Story test for stories/run-an-event-from-the-organizer-console. It runs the
// use cases the console calls along the story (SeatTypeUseCase,
// SeriesUseCase, TicketStockUseCase, TicketSaleUseCase, ScannerUseCase, and
// TicketUseCase for the door) over the real repositories and local Postgres.
// The venue is already known by its place id, so the map catalog is never
// called; messaging is replaced by storyPublisher. Navigation, dialogs and
// snackbars are the frontend's and are not exercised here.

// consoleAdmitProcedure is the procedure a scanner device signs for Admit.
const consoleAdmitProcedure = "/liverty_music.rpc.scanner.v1.ScannerService/Admit"

// consoleOpenProcedure is the procedure a scanner device signs for Open.
const consoleOpenProcedure = "/liverty_music.rpc.scanner.v1.ScannerService/Open"

type consoleStory struct {
	t         *testing.T
	ctx       context.Context
	publisher *storyPublisher
	orgID     string
	artistID  string
	placeID   string
	// eventDay is the next 20 December, the date of the story's event.
	eventDay time.Time

	series    usecase.SeriesUseCase
	seatTypes usecase.SeatTypeUseCase
	stocks    usecase.TicketStockUseCase
	sales     usecase.TicketSaleUseCase
	scanners  usecase.ScannerUseCase
	tickets   usecase.TicketUseCase
}

func newConsoleStory(t *testing.T) *consoleStory {
	t.Helper()
	cleanDatabase(t)
	logger, err := logging.New()
	require.NoError(t, err)

	now := time.Now().In(jst)
	year := now.Year()
	if !now.Before(time.Date(year, 12, 1, 0, 0, 0, 0, jst)) {
		year++
	}
	s := &consoleStory{
		t:         t,
		ctx:       context.Background(),
		publisher: &storyPublisher{},
		orgID:     seedOrganizer(t),
		artistID:  seedArtist(t, "XX", uuid.NewV4().String()),
		placeID:   "ChIJ-console-" + uuid.NewV7().String(),
		eventDay:  time.Date(year, 12, 20, 0, 0, 0, 0, jst),
	}
	_, err = rdb.NewOrganizerRepository(testDB).SetBusinessDetails(s.ctx, s.orgID, entity.BusinessDetails{
		LegalName:           "株式会社テスト興行",
		RepresentativeName:  "代表 太郎",
		BusinessAddress:     "東京都渋谷区1-1-1",
		BusinessPhoneNumber: "+81312345678",
		ContactEmail:        "contact@example.test",
	})
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(s.ctx, `INSERT INTO organizer_artists (organizer_id, artist_id) VALUES ($1, $2)`, s.orgID, s.artistID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(s.ctx, `INSERT INTO venues (id, name, admin_area, google_place_id) VALUES ($1, 'Zepp Haneda (TOKYO)', 'JP-13', $2)`,
		entity.NewID(), s.placeID)
	require.NoError(t, err)

	clock := usecase.Clock(time.Now)
	seriesRepo := rdb.NewSeriesRepository(testDB)
	eventRepo := rdb.NewEventRepository(testDB)
	stockRepo := rdb.NewTicketStockRepository(testDB)
	seatTypeRepo := rdb.NewSeatTypeRepository(testDB)
	organizerRepo := rdb.NewOrganizerRepository(testDB)
	saleRepo := rdb.NewTicketSaleRepository(testDB)
	eventOrganizerRepo := rdb.NewEventOrganizerRepository(testDB)
	eventState := rdb.NewEventPublishStateRepository(testDB)
	orderRepo := orderRepository(t, rdb.NewOrderRepository(testDB))
	scannerRepo := rdb.NewScannerRepository(testDB)

	s.series = usecase.NewSeriesUseCase(seriesRepo, rdb.NewSeriesImageRepository(testDB), rdb.NewVenueRepository(testDB), nil,
		organizerRepo, saleRepo, nil, usecase.StorageBuckets{}, s.publisher, logger, time.Now)
	s.seatTypes = usecase.NewSeatTypeUseCase(seatTypeRepo, seriesRepo)
	s.stocks = usecase.NewTicketStockUseCase(stockRepo, seatTypeRepo, eventRepo, seriesRepo, eventOrganizerRepo, clock)
	s.sales = usecase.NewTicketSaleUseCase(saleRepo, stockRepo, rdb.NewLotteryEntryRepository(testDB), orderRepo, seriesRepo,
		organizerRepo, eventRepo, eventState, rdb.NewEventStartTimeRepository(testDB), clock)
	s.scanners = usecase.NewScannerUseCase(scannerRepo, eventRepo, eventOrganizerRepo, eventState, logger)
	s.tickets = usecase.NewTicketUseCase(orderRepo, rdb.NewTicketRepository(testDB), scannerRepo, eventRepo,
		rdb.NewWalletPublicKeyRepository(testDB), rdb.NewAdmissionRepository(testDB), rdb.NewRejectedScanRepository(testDB),
		s.publisher, logger)
	return s
}

// at returns the event day's time hh:mm in Japan.
func (s *consoleStory) at(hh, mm int) time.Time {
	return s.eventDay.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
}

// draftConcert creates the draft concert XX 単独公演 with its 20 December
// event, doors 18:00 and start 19:00, and returns the series and event ids.
func (s *consoleStory) draftConcert() (string, string) {
	s.t.Helper()
	visibility := entity.SeriesVisibilityPublic
	start, doors := s.at(19, 0), s.at(18, 0)
	series, concerts, err := s.series.Create(s.ctx, s.orgID,
		&entity.Series{Title: "XX 単独公演", Type: entity.SeriesTypeSingle, Visibility: &visibility},
		[]*usecase.DraftEventInput{{
			VenueName: "Zepp Haneda",
			PlaceID:   &s.placeID,
			LocalDate: time.Date(s.eventDay.Year(), 12, 20, 0, 0, 0, 0, time.UTC),
			StartTime: &start,
			OpenTime:  &doors,
		}}, []string{s.artistID})
	require.NoError(s.t, err)
	events := entity.EventsOf(concerts)
	require.Len(s.t, events, 1)
	return series.ID, events[0].ID
}

// authoredEvent returns the event as the console's concert list shows it.
func (s *consoleStory) authoredEvent(eventID string) *entity.Event {
	s.t.Helper()
	_, concerts, err := s.series.List(s.ctx, s.orgID)
	require.NoError(s.t, err)
	for _, e := range entity.EventsOf(concerts) {
		if e.ID == eventID {
			return e
		}
	}
	s.t.Fatalf("event %s is not among the organizer's events", eventID)
	return nil
}

// runConsole runs the whole console flow: it drafts XX 単独公演, adds the seat
// type S席 at 8000 yen, publishes the 20 December event, stocks 100 S席
// tickets, creates the lottery ファンクラブ先行 and issues 受付1. It returns
// the event id and the issued scanner.
func (s *consoleStory) runConsole() (string, *entity.Scanner) {
	s.t.Helper()
	seriesID, eventID := s.draftConcert()

	seatType, err := s.seatTypes.Create(s.ctx, s.orgID, seriesID, "S席", 8000)
	require.NoError(s.t, err)
	_, _, err = s.series.Publish(s.ctx, s.orgID, seriesID, []string{eventID})
	require.NoError(s.t, err)
	_, err = s.stocks.Create(s.ctx, s.orgID, eventID, seatType.ID, 100)
	require.NoError(s.t, err)

	open := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	_, err = s.sales.Create(s.ctx, s.orgID, usecase.CreateTicketSaleInput{
		SeriesID:  seriesID,
		Name:      "ファンクラブ先行",
		Method:    entity.TicketSaleMethodLottery,
		SaleStart: open,
		SaleEnd:   open.Add(7 * 24 * time.Hour),
		EventIDs:  []string{eventID},
	})
	require.NoError(s.t, err)

	scanner, err := s.scanners.Create(s.ctx, s.orgID, eventID)
	require.NoError(s.t, err)
	return eventID, scanner
}

func TestStory_RunAnEventFromTheOrganizerConsole(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("the whole flow publishes, stocks, sells and staffs the event", func(t *testing.T) {
		// @spec stories/run-an-event-from-the-organizer-console "Whole flow on a PC"
		s := newConsoleStory(t)
		eventID, issued := s.runConsole()

		event := s.authoredEvent(eventID)
		require.NotNil(t, event.PublishState)
		assert.Equal(t, entity.PublishStatePublished, *event.PublishState,
			"the event is published, so Home no longer lists its draft item")
		require.NotNil(t, event.PerAccountLimit)
		assert.Equal(t, entity.EventDefaultPerAccountLimit, *event.PerAccountLimit)

		stocks, err := s.stocks.ListByEvent(s.ctx, s.orgID, eventID)
		require.NoError(t, err)
		require.Len(t, stocks, 1, "the Sales tab shows one stock")
		assert.Equal(t, "S席", stocks[0].SeatType.Name)
		assert.Equal(t, int64(8000), stocks[0].SeatType.Price)
		assert.Equal(t, 100, stocks[0].Quantity)
		assert.Zero(t, stocks[0].SoldCount)
		assert.Zero(t, stocks[0].HeldCount)

		own, err := s.sales.ListOwnByEvent(s.ctx, s.orgID, eventID)
		require.NoError(t, err)
		require.Len(t, own.Sales, 1, "the Sales tab lists the lottery")
		assert.Equal(t, "ファンクラブ先行", own.Sales[0].Name)
		assert.Equal(t, entity.TicketSaleMethodLottery, own.Sales[0].Method)
		assert.Equal(t, []string{eventID}, own.Sales[0].EventIDs)
		require.Len(t, own.LotteryEntryStats, 1, "with the entry stats of its S席 stock")
		assert.Equal(t, stocks[0].ID, own.LotteryEntryStats[0].Stock.ID)
		assert.Equal(t, own.Sales[0].ID, own.LotteryEntryStats[0].SaleID)

		scanners, err := s.scanners.ListByEvent(s.ctx, s.orgID, eventID)
		require.NoError(t, err)
		require.Len(t, scanners, 1, "the Reception tab lists one scanner")
		assert.Equal(t, 1, scanners[0].Number, "named 受付1")
		assert.Equal(t, entity.ScannerStatusUnused, scanners[0].Status, "as 未使用")
		assert.NotEmpty(t, scanners[0].LinkToken, "with its link")
		assert.Equal(t, issued.LinkToken, scanners[0].LinkToken)
	})

	t.Run("a declined publish leaves the event draft and announces nothing", func(t *testing.T) {
		// @spec stories/run-an-event-from-the-organizer-console "Publish declined"
		// Declining the dialog sends no Publish; the backend half is that the
		// event then stays draft, nothing is announced, and the steps that
		// need a published event stay closed.
		s := newConsoleStory(t)
		seriesID, eventID := s.draftConcert()
		seatType, err := s.seatTypes.Create(s.ctx, s.orgID, seriesID, "S席", 8000)
		require.NoError(t, err)

		event := s.authoredEvent(eventID)
		require.NotNil(t, event.PublishState)
		assert.Equal(t, entity.PublishStateDraft, *event.PublishState)
		assert.Empty(t, s.publisher.take(entity.SubjectConcertCreated), "no follower is notified")

		// A draft event may be stocked, but no sale opens on it.
		_, err = s.stocks.Create(s.ctx, s.orgID, eventID, seatType.ID, 100)
		require.NoError(t, err)
		open := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
		_, err = s.sales.Create(s.ctx, s.orgID, usecase.CreateTicketSaleInput{
			SeriesID: seriesID, Name: "ファンクラブ先行", Method: entity.TicketSaleMethodLottery,
			SaleStart: open, SaleEnd: open.Add(7 * 24 * time.Hour), EventIDs: []string{eventID},
		})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "a draft event cannot be put on sale")
		_, err = s.scanners.Create(s.ctx, s.orgID, eventID)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "nor given a scanner")
	})

	t.Run("an added seat type keeps its name and price", func(t *testing.T) {
		// @spec stories/run-an-event-from-the-organizer-console "Seat type confirmed as fixed"
		// The dialog and snackbar are the frontend's. The backend half: the
		// seat type is stored with the confirmed name and price, the console
		// lists it so, and the use case offers no edit of either.
		s := newConsoleStory(t)
		seriesID, _ := s.draftConcert()

		created, err := s.seatTypes.Create(s.ctx, s.orgID, seriesID, "S席", 8000)
		require.NoError(t, err)

		listed, err := s.seatTypes.ListBySeries(s.ctx, s.orgID, seriesID)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		assert.Equal(t, created.ID, listed[0].ID)
		assert.Equal(t, "S席", listed[0].Name)
		assert.Equal(t, int64(8000), listed[0].Price)

		_, err = s.seatTypes.Create(s.ctx, s.orgID, seriesID, "S席", 9000)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists, "a second S席 cannot replace the first")
	})

	t.Run("a revoked scanner can no longer admit anyone", func(t *testing.T) {
		// @spec stories/run-an-event-from-the-organizer-console "Scanner revoked"
		s := newConsoleStory(t)
		eventID, issued := s.runConsole()

		// A fan holds two tickets of the event with a registered wallet key.
		fanKey := testutil.NewDeviceKey(t)
		fanID := entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		tickets := seedIssuedTickets(t, eventID, string(fanID), 2)
		_, _, err := rdb.NewWalletPublicKeyRepository(testDB).Register(s.ctx, fanID, fanKey.PublicKey(t), time.Now())
		require.NoError(t, err)

		// Staff open 受付1 on their device on the event day.
		staff := testutil.NewDeviceKey(t)
		openTime := s.at(17, 0)
		call := staff.SignCall(t, consoleOpenProcedure, issued.LinkToken, staff.PublicKey(t).Base64URL(), openTime)
		_, err = s.scanners.Open(s.ctx, usecase.OpenScannerInput{
			Procedure: consoleOpenProcedure, LinkToken: issued.LinkToken, PublicKey: staff.PublicKey(t),
			SignTime: call.SignTime, Signature: call.Signature, Now: openTime,
		})
		require.NoError(t, err)

		admit := func(ticket entity.TicketID, now time.Time) (*usecase.AdmitResult, error) {
			code := fanKey.SignCode(t, &entity.AdmissionCode{UserID: fanID, EventID: eventID, TicketIDs: []entity.TicketID{ticket}, SignedTime: now})
			call := staff.SignCall(t, consoleAdmitProcedure, issued.LinkToken, code, now)
			return s.tickets.Admit(s.ctx, usecase.AdmitInput{
				Procedure: consoleAdmitProcedure, LinkToken: issued.LinkToken,
				SignTime: call.SignTime, Signature: call.Signature, ScannedText: code, Now: now,
			})
		}
		got, err := admit(tickets[0], s.at(18, 30))
		require.NoError(t, err)
		require.Equal(t, 1, got.AdmittedTicketCount, "受付1 admits before it is revoked")

		revoked, err := s.scanners.Revoke(s.ctx, s.orgID, issued.ID, s.at(18, 35))
		require.NoError(t, err)
		assert.Equal(t, entity.ScannerStatusRevoked, revoked.Status, "受付1 is 取り消し済み")

		scanners, err := s.scanners.ListByEvent(s.ctx, s.orgID, eventID)
		require.NoError(t, err)
		require.Len(t, scanners, 1)
		assert.Equal(t, entity.ScannerStatusRevoked, scanners[0].Status)
		assert.Empty(t, scanners[0].LinkToken, "its link is no longer shown")

		_, err = admit(tickets[1], s.at(18, 40))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "the device that opened 受付1 can no longer admit anyone")
		var admissions int
		require.NoError(t, testDB.Pool.QueryRow(s.ctx, `SELECT count(*) FROM admissions WHERE event_id = $1`, eventID).Scan(&admissions))
		assert.Equal(t, 1, admissions, "only the scan before the revoke admitted")
	})
}
