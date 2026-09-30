package writebatcher

// Close signals shutdown: it closes the input channel, waits for the worker to
// flush in-memory channel items and exit, cancels the context, then closes the
// dque handle (if configured) without draining persisted overflow items.
// After Close returns, all subsequent Submit calls return ErrClosed.
//
// Close is safe to call multiple times; after the first call it returns nil
// immediately without blocking.
func (wb *WriteBatcher[T]) Close() error {
	wb.mu.Lock()
	if wb.closed.Load() {
		wb.mu.Unlock()
		return nil
	}
	wb.closed.Store(true)
	close(wb.ch)
	wb.mu.Unlock()

	// Wait for any in-flight overflow Submits to complete before the
	// worker drains remaining items. After this returns, all overflowed
	// items are enqueued to the dque and will be drained by the worker.
	wb.overflowWG.Wait()

	<-wb.done
	wb.cancel()
	if wb.dq != nil {
		_ = wb.dq.Close()
	}
	return nil
}
