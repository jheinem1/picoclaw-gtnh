package com.seibel.distanthorizons.core.api.internal.chunkUpdating;

import com.seibel.distanthorizons.core.pos.DhChunkPos;
import com.seibel.distanthorizons.core.wrapperInterfaces.chunk.IChunkWrapper;

import java.util.Collections;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentMap;

/**
 * Local static-cache patch for the Fishtank imported DH server cache.
 *
 * GTNH/DH 3.0.0-b-dev writes live multiplayer chunk wrappers back into the
 * server cache, and those wrappers serialize biomes as EMPTY. For an imported
 * pregenerated cache, that corrupts the clean LOD rows as soon as the player
 * joins the server. This replacement keeps the class API intact but disables
 * local live chunk update processing so DH renders the imported cache as static
 * data.
 */
public class ChunkUpdateQueueManager {
    public static final int MAX_UPDATING_CHUNK_COUNT_PER_THREAD_AND_PLAYER = 1000;
    public static final int MIN_MS_BETWEEN_OVERLOADED_LOG_MESSAGE = 30000;
    public static final ChunkUpdateQueueManager INSTANCE = new ChunkUpdateQueueManager();
    public static final Set<String> LOGGED_GET_ERROR_MESSAGES =
            Collections.newSetFromMap(new ConcurrentHashMap<>());

    public final ChunkPosQueue updateQueue = new ChunkPosQueue();
    public final ChunkPosQueue preUpdateQueue = new ChunkPosQueue();
    public final ConcurrentMap<DhChunkPos, IChunkWrapper> queuedChunkWrapperByChunkPos = new ConcurrentHashMap<>();
    public int maxSize = MAX_UPDATING_CHUNK_COUNT_PER_THREAD_AND_PLAYER;
    public long lastMsTimeShownActiveInF3Screen = System.currentTimeMillis();

    public boolean contains(DhChunkPos chunkPos) {
        return false;
    }

    public void clear() {
        this.updateQueue.clear();
        this.preUpdateQueue.clear();
        this.queuedChunkWrapperByChunkPos.clear();
    }

    public int getQueuedCount() {
        return 0;
    }

    public boolean updateQueuesEmpty() {
        return true;
    }

    public void addItemToPreUpdateQueue(DhChunkPos chunkPos, ChunkUpdateData updateData) {
        // Static imported server-cache mode: never enqueue live client chunks.
    }

    public void addItemToUpdateQueue(DhChunkPos chunkPos, ChunkUpdateData updateData) {
        // Static imported server-cache mode: never enqueue live client chunks.
    }

    public IChunkWrapper tryGetChunk(DhChunkPos chunkPos) {
        return null;
    }

    public void addPosToIgnore(DhChunkPos chunkPos) {
        // No-op; updates are globally disabled for this local static cache.
    }

    public void removePosToIgnore(DhChunkPos chunkPos) {
        // No-op; updates are globally disabled for this local static cache.
    }

    public void processQueue() {
        // No-op; prevents updateChunkAsync writes that replace clean biomes with EMPTY.
    }

    public void setCenter(DhChunkPos center) {
        this.updateQueue.setCenter(center);
        this.preUpdateQueue.setCenter(center);
    }

    public String getDebugMenuString() {
        return "Queued chunk updates: static cache mode";
    }
}
