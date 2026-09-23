# Delta: download-progress-and-queue

## ADDED Requirements

### Requirement: download chunk progress
The system SHALL report download progress incrementally: `DLItem.Bytes` MUST be updated on each read chunk, not only after completion.

#### Scenario: Progress while downloading
- **GIVEN** a download job with a `DLItem` whose `Total > 0` (Content-Length known)
- **WHEN** the file is being downloaded
- **THEN** `DLItem.Bytes` increases monotonically as bytes arrive

#### Scenario: Unknown total
- **GIVEN** a `DLItem` with `Total == 0` (Content-Length absent)
- **WHEN** the download is in progress
- **THEN** progress is reported as bytes read, and completion sets `Bytes == Total` when known

### Requirement: persistent download snapshots
The system SHALL persist download job snapshots to a JSON store with atomic writes and TTL.

#### Scenario: Snapshot survives reload
- **GIVEN** an in-progress download job persisted to the store
- **WHEN** the API is queried for that job after a page reload
- **THEN** the stored `Bytes`/`Status` is returned, not reset to zero

### Requirement: rate-limited downloads
The system SHALL limit the number of concurrent downloads via a bounded worker pool.

#### Scenario: Bounded concurrency
- **GIVEN** `provision.max_concurrent == N` (default 2)
- **WHEN** many models need downloading
- **THEN** no more than N downloads run simultaneously

## MODIFIED Requirements

### Requirement: download status API
The `GET /comfy/downloads/{id}` endpoint MUST continue to return `items[].bytes`, `items[].total`, and `status` (backward-compatible), and MAY include a computed percentage when `total > 0`.

#### Scenario: Backward-compatible status
- **GIVEN** a client polling `GET /comfy/downloads/{id}`
- **WHEN** the download is in progress
- **THEN** the response includes `bytes`, `total`, `status`, and optionally `progress`

### Requirement: ComfyUI queue view
The system SHALL expose a ComfyUI queue summary via a new `GET /comfy/queue` endpoint.

#### Scenario: Queue summary
- **GIVEN** a ComfyUI instance with running and pending jobs
- **WHEN** `GET /comfy/queue` is called
- **THEN** the response includes counts of running and pending jobs
