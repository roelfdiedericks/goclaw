package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/roelfdiedericks/goclaw/internal/paths"
)

// LinkDevice performs QR code pairing for a new WhatsApp device.
// Displays the QR code in the terminal and waits for the user to scan it.
func LinkDevice() error {
	dbPath, err := paths.DataPath("whatsapp.db")
	if err != nil {
		return fmt.Errorf("failed to resolve db path: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}
	defer db.Close()

	storeLog := &goclawLogger{module: "store"}
	container := sqlstore.NewWithDB(db, "sqlite3", storeLog)

	if err := container.Upgrade(context.Background()); err != nil {
		return fmt.Errorf("failed to upgrade store: %w", err)
	}

	// Remove any stale device entries from previous pairing attempts.
	// GetFirstDevice would otherwise return an old invalidated session,
	// causing 401 errors when the gateway tries to connect.
	oldDevices, err := container.GetAllDevices(context.Background())
	if err != nil {
		return fmt.Errorf("failed to list existing devices: %w", err)
	}
	for _, d := range oldDevices {
		jid := "(unknown)"
		if d.ID != nil {
			jid = d.ID.String()
		}
		fmt.Printf("Removing stale device: %s\n", jid)
		_ = d.Delete(context.Background())
	}

	clientLog := &goclawLogger{module: "client"}
	device := container.NewDevice()
	client := whatsmeow.NewClient(device, clientLog)

	// Channel that fires once the client is fully connected and synced.
	// The QR "success" event only means the scan was accepted — the client
	// still needs to complete initial sync (pre-keys, identity, app state).
	// Disconnecting before Connected fires leaves the pairing incomplete.
	connectedCh := make(chan struct{}, 1)
	client.AddEventHandler(func(evt interface{}) {
		if _, ok := evt.(*events.Connected); ok {
			select {
			case connectedCh <- struct{}{}:
			default:
			}
		}
	})

	qrChan, err := client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get QR channel: %w", err)
	}

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	fmt.Println("Scan the QR code below with your WhatsApp app:")
	fmt.Println("  WhatsApp > Settings > Linked Devices > Link a Device")
	fmt.Println()

	for item := range qrChan {
		if item.Event == "code" {
			qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
			fmt.Println()
			fmt.Println("Waiting for scan...")
		} else if item.Event == "success" {
			fmt.Println("\nScan accepted, completing initial sync...")

			// Wait for the full handshake to finish
			select {
			case <-connectedCh:
				// Fully synced
			case <-time.After(30 * time.Second):
				client.Disconnect()
				return fmt.Errorf("timed out waiting for initial sync — try again")
			}

			fmt.Printf("Paired successfully! JID: %s\n", client.Store.ID)
			fmt.Println("You can now enable WhatsApp in goclaw.json and start the gateway.")
			client.Disconnect()
			return nil
		} else if item.Event == "timeout" {
			client.Disconnect()
			return fmt.Errorf("QR code expired — run the command again")
		} else {
			client.Disconnect()
			return fmt.Errorf("pairing failed: %s", item.Event)
		}
	}

	client.Disconnect()
	return fmt.Errorf("QR channel closed unexpectedly")
}

// UnlinkDevice removes the stored WhatsApp session, requiring re-pairing.
func UnlinkDevice() error {
	dbPath, err := paths.DataPath("whatsapp.db")
	if err != nil {
		return fmt.Errorf("failed to resolve db path: %w", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no WhatsApp session found (no %s)", dbPath)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}
	defer db.Close()

	storeLog := &goclawLogger{module: "store"}
	container := sqlstore.NewWithDB(db, "sqlite3", storeLog)

	if err := container.Upgrade(context.Background()); err != nil {
		return fmt.Errorf("failed to upgrade store: %w", err)
	}

	devices, err := container.GetAllDevices(context.Background())
	if err != nil {
		return fmt.Errorf("failed to list devices: %w", err)
	}

	if len(devices) == 0 {
		return fmt.Errorf("no paired devices found")
	}

	for _, device := range devices {
		jid := "(unknown)"
		if device.ID != nil {
			jid = device.ID.String()
		}
		if err := device.Delete(context.Background()); err != nil {
			return fmt.Errorf("failed to delete device %s: %w", jid, err)
		}
		fmt.Printf("Removed device: %s\n", jid)
	}

	fmt.Println("WhatsApp session cleared. Run 'goclaw whatsapp link' to re-pair.")
	return nil
}

// DeviceStatus shows the current WhatsApp pairing status.
func DeviceStatus() error {
	dbPath, err := paths.DataPath("whatsapp.db")
	if err != nil {
		return fmt.Errorf("failed to resolve db path: %w", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("Status: Not paired (no session database)")
		return nil
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}
	defer db.Close()

	storeLog := waLog.Noop
	container := sqlstore.NewWithDB(db, "sqlite3", storeLog)

	if err := container.Upgrade(context.Background()); err != nil {
		return fmt.Errorf("failed to upgrade store: %w", err)
	}

	devices, err := container.GetAllDevices(context.Background())
	if err != nil {
		return fmt.Errorf("failed to list devices: %w", err)
	}

	if len(devices) == 0 {
		fmt.Println("Status: Not paired")
		fmt.Println("Run 'goclaw whatsapp link' to pair a device.")
		return nil
	}

	for _, device := range devices {
		fmt.Printf("Status: Paired\n")
		fmt.Printf("  JID: %s\n", device.ID)
	}
	return nil
}
