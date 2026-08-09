package samp

import (
	"context"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
)

func (c *Client) setDrunkLevel(level uint32) {
	c.stateMu.Lock()
	c.drunkLevel = level
	c.drunkLevelSet = true
	c.stateMu.Unlock()
}

func (c *Client) statsLoop() {
	ticker := time.NewTicker(statsUpdateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			level, active := c.advanceDrunkLevel(targetFramesPerSecond)
			if active {
				_ = c.sendStats(c.ctx, level)
			}
		}
	}
}

func (c *Client) advanceDrunkLevel(frames uint32) (uint32, bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.drunkLevelSet {
		return 0, false
	}
	if frames >= c.drunkLevel {
		c.drunkLevel = 0
	} else {
		c.drunkLevel -= frames
	}
	return c.drunkLevel, true
}

func (c *Client) sendStats(ctx context.Context, drunkLevel uint32) error {
	w := raknet.Writer{}
	w.Uint8(packetStatsUpdate)
	w.Uint32(uint32(defaultPlayerMoney))
	w.Uint32(drunkLevel)
	return c.conn.Write(ctx, w.Bytes(), raknet.Unreliable)
}
