package com.seibel.distanthorizons.core.file.fullDatafile.V2;

import com.seibel.distanthorizons.core.dataObjects.fullData.sources.FullDataSourceV2;
import com.seibel.distanthorizons.core.file.fullDatafile.IDataSourceUpdateListenerFunc;
import com.seibel.distanthorizons.core.render.renderer.AbstractDebugWireframeRenderer;
import com.seibel.distanthorizons.core.render.renderer.IDebugRenderable;
import com.seibel.distanthorizons.core.util.threading.PositionalLockProvider;

import java.util.ArrayList;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;

public class FullDataUpdaterV2 implements IDebugRenderable, AutoCloseable {
    protected final PositionalLockProvider updateLockProvider;
    public final Set<Long> lockedPosSet;
    public final ArrayList<IDataSourceUpdateListenerFunc<FullDataSourceV2>> dateSourceUpdateListeners;
    private final AtomicBoolean isShutdownRef;

    public FullDataUpdaterV2(FullDataSourceProviderV2 provider, String levelId) {
        this.updateLockProvider = new PositionalLockProvider();
        this.lockedPosSet = ConcurrentHashMap.newKeySet();
        this.dateSourceUpdateListeners = new ArrayList<>();
        this.isShutdownRef = new AtomicBoolean(false);
    }

    public CompletableFuture<Void> updateDataSourceAsync(FullDataSourceV2 dataSource) {
        return CompletableFuture.completedFuture(null);
    }

    public void updateDataSource(FullDataSourceV2 dataSource) {
        // Static-cache mode: keep imported DH data read-only during multiplayer joins.
    }

    public void debugRender(AbstractDebugWireframeRenderer renderer) {
    }

    public void close() {
        this.isShutdownRef.set(true);
    }
}
