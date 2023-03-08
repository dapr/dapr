/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reminders

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueue(t *testing.T) {
	queue := NewQueue()

	// Add 5 reminders, which are not in order
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	queue.Insert(newTestReminder(3, "2023-03-03T03:03:03Z"), false)
	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)
	queue.Insert(newTestReminder(5, "2029-09-09T09:09:09Z"), false)
	queue.Insert(newTestReminder(4, "2024-04-04T04:04:04Z"), false)

	require.Equal(t, 5, queue.Len())

	i := 0
	for {
		// Pop an element from the queue
		r := queue.Pop()

		if i < 5 {
			require.NotNil(t, r)
		} else {
			require.Nil(t, r)
			break
		}
		i++

		// Results should be in order
		ri, err := strconv.Atoi(r.Name)
		require.NoError(t, err)
		assert.Equal(t, i, ri)
	}
}

func TestQueueSkipDuplicates(t *testing.T) {
	queue := NewQueue()

	// Add 2 reminders
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)

	require.Equal(t, 2, queue.Len())

	// Add a duplicate reminder (same actor type, actor ID, name), but different time
	queue.Insert(newTestReminder(2, "2029-09-09T09:09:09Z"), false)

	require.Equal(t, 2, queue.Len())

	// Pop the items and check only the 2 original ones were in the queue
	popAndCompare(t, &queue, 1, "2021-01-01T01:01:01Z")
	popAndCompare(t, &queue, 2, "2022-02-02T02:02:02Z")

	r := queue.Pop()
	require.Nil(t, r)
}

func TestQueueReplaceDuplicates(t *testing.T) {
	queue := NewQueue()

	// Add 2 reminders
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)

	require.Equal(t, 2, queue.Len())

	// Replace a reminder
	queue.Insert(newTestReminder(1, "2029-09-09T09:09:09Z"), true)

	require.Equal(t, 2, queue.Len())

	// Pop the items and validate the new order
	popAndCompare(t, &queue, 2, "2022-02-02T02:02:02Z")
	popAndCompare(t, &queue, 1, "2029-09-09T09:09:09Z")

	r := queue.Pop()
	require.Nil(t, r)
}

func TestAddToQueue(t *testing.T) {
	queue := NewQueue()

	// Add 5 reminders, which are not in order
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	queue.Insert(newTestReminder(5, "2023-03-03T03:03:03Z"), false)
	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)
	queue.Insert(newTestReminder(8, "2029-09-09T09:09:09Z"), false)
	queue.Insert(newTestReminder(7, "2024-04-04T04:04:04Z"), false)

	require.Equal(t, 5, queue.Len())

	// Pop 2 elements from the queue
	for i := 1; i <= 2; i++ {
		r := queue.Pop()
		require.NotNil(t, r)

		ri, err := strconv.Atoi(r.Name)
		require.NoError(t, err)
		assert.Equal(t, i, ri)
	}

	// Add 4 more elements
	// Two are at the very beginning (including one that had the same time as one popped before)
	// One is in the middle
	// One is at the end
	queue.Insert(newTestReminder(3, "2021-01-01T01:01:01Z"), false)
	queue.Insert(newTestReminder(4, "2021-01-11T11:11:11Z"), false)
	queue.Insert(newTestReminder(6, "2023-03-13T13:13:13Z"), false)
	queue.Insert(newTestReminder(9, "2030-10-30T10:10:10Z"), false)

	require.Equal(t, 7, queue.Len())

	// Pop all the remaining elements and make sure they're in order
	for i := 3; i <= 9; i++ {
		r := queue.Pop()
		require.NotNil(t, r)

		ri, err := strconv.Atoi(r.Name)
		require.NoError(t, err)
		assert.Equal(t, i, ri)
	}

	// Queue should be empty now
	r := queue.Pop()
	require.Nil(t, r)
	require.Equal(t, 0, queue.Len())
}

func TestRemoveFromQueue(t *testing.T) {
	queue := NewQueue()

	// Add 5 reminders, which are not in order
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	queue.Insert(newTestReminder(3, "2023-03-03T03:03:03Z"), false)
	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)
	queue.Insert(newTestReminder(5, "2029-09-09T09:09:09Z"), false)
	queue.Insert(newTestReminder(4, "2024-04-04T04:04:04Z"), false)

	require.Equal(t, 5, queue.Len())

	// Pop 2 elements from the queue
	for i := 1; i <= 2; i++ {
		r := queue.Pop()
		require.NotNil(t, r)

		ri, err := strconv.Atoi(r.Name)
		require.NoError(t, err)
		assert.Equal(t, i, ri)
	}

	require.Equal(t, 3, queue.Len())

	// Remove the reminder with number "4"
	queue.Remove(newTestReminder(4, 0)) // DueTime is irrelevant here

	// Removing non-existing reminders is a nop
	queue.Remove(newTestReminder(10, 0)) // DueTime is irrelevant here

	require.Equal(t, 2, queue.Len())

	// Pop all the remaining elements and make sure they're in order
	popAndCompare(t, &queue, 3, "2023-03-03T03:03:03Z")
	popAndCompare(t, &queue, 5, "2029-09-09T09:09:09Z")

	r := queue.Pop()
	require.Nil(t, r)
}

func TestUpdateInQueue(t *testing.T) {
	queue := NewQueue()

	// Add 5 reminders, which are not in order
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	queue.Insert(newTestReminder(3, "2023-03-03T03:03:03Z"), false)
	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)
	queue.Insert(newTestReminder(5, "2029-09-09T09:09:09Z"), false)
	queue.Insert(newTestReminder(4, "2024-04-04T04:04:04Z"), false)

	require.Equal(t, 5, queue.Len())

	// Pop 2 elements from the queue
	for i := 1; i <= 2; i++ {
		r := queue.Pop()
		require.NotNil(t, r)

		ri, err := strconv.Atoi(r.Name)
		require.NoError(t, err)
		assert.Equal(t, i, ri)
	}

	require.Equal(t, 3, queue.Len())

	// Update the reminder with number "4" but maintain priority
	queue.Update(newTestReminder(4, "2024-04-04T14:14:14Z"))

	// Update the reminder with number "5" and increase the priority
	queue.Update(newTestReminder(5, "2021-01-01T01:01:01Z"))

	// Updating non-existing reminders is a nop
	queue.Update(newTestReminder(10, "2021-01-01T01:01:01Z"))

	require.Equal(t, 3, queue.Len())

	// Pop all the remaining elements and make sure they're in order
	popAndCompare(t, &queue, 5, "2021-01-01T01:01:01Z") // 5 comes before 3 now
	popAndCompare(t, &queue, 3, "2023-03-03T03:03:03Z")
	popAndCompare(t, &queue, 4, "2024-04-04T14:14:14Z")

	r := queue.Pop()
	require.Nil(t, r)
}

func TestQueuePeek(t *testing.T) {
	queue := NewQueue()

	// Peeking an empty queue returns nil
	r := queue.Peek()
	require.Nil(t, r)

	// Add 6 reminders, which are not in order
	queue.Insert(newTestReminder(2, "2022-02-02T02:02:02Z"), false)
	peekAndCompare(t, &queue, 2, "2022-02-02T02:02:02Z")

	queue.Insert(newTestReminder(3, "2023-03-03T03:03:03Z"), false)
	peekAndCompare(t, &queue, 2, "2022-02-02T02:02:02Z")

	queue.Insert(newTestReminder(1, "2021-01-01T01:01:01Z"), false)
	peekAndCompare(t, &queue, 1, "2021-01-01T01:01:01Z")

	queue.Insert(newTestReminder(5, "2029-09-09T09:09:09Z"), false)
	peekAndCompare(t, &queue, 1, "2021-01-01T01:01:01Z")

	queue.Insert(newTestReminder(4, "2024-04-04T04:04:04Z"), false)
	peekAndCompare(t, &queue, 1, "2021-01-01T01:01:01Z")

	queue.Insert(newTestReminder(6, "2019-01-19T01:01:01Z"), false)
	peekAndCompare(t, &queue, 6, "2019-01-19T01:01:01Z")

	// Pop from the queue
	popAndCompare(t, &queue, 6, "2019-01-19T01:01:01Z")
	peekAndCompare(t, &queue, 1, "2021-01-01T01:01:01Z")

	// Update a reminder to bring it to first
	queue.Update(newTestReminder(2, "2019-01-19T01:01:01Z"))
	peekAndCompare(t, &queue, 2, "2019-01-19T01:01:01Z")

	// Replace the first reminder to push it back
	queue.Insert(newTestReminder(2, "2039-01-19T01:01:01Z"), true)
	peekAndCompare(t, &queue, 1, "2021-01-01T01:01:01Z")
}

func newTestReminder(n int, dueTime any) *Reminder {
	r := &Reminder{
		Name:      strconv.Itoa(n),
		ActorID:   "id",
		ActorType: "type",
	}

	switch t := dueTime.(type) {
	case time.Time:
		r.RegisteredTime = t
	case string:
		r.RegisteredTime, _ = time.Parse(time.RFC3339, t)
	case int64:
		r.RegisteredTime = time.Unix(t, 0)
	}

	return r
}

func popAndCompare(t *testing.T, queue *Queue, expectN int, expectDueTime string) {
	t.Helper()

	r := queue.Pop()
	require.NotNil(t, r)
	assert.Equal(t, r.Name, strconv.Itoa(expectN))
	assert.Equal(t, r.RegisteredTime.Format(time.RFC3339), expectDueTime)
}

func peekAndCompare(t *testing.T, queue *Queue, expectN int, expectDueTime string) {
	t.Helper()

	r := queue.Peek()
	require.NotNil(t, r)
	assert.Equal(t, r.Name, strconv.Itoa(expectN))
	assert.Equal(t, r.RegisteredTime.Format(time.RFC3339), expectDueTime)
}
