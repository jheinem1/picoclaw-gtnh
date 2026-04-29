package greggpt.meexport;

import cpw.mods.fml.common.FMLCommonHandler;
import cpw.mods.fml.common.Mod;
import cpw.mods.fml.common.eventhandler.SubscribeEvent;
import cpw.mods.fml.common.gameevent.TickEvent;
import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.io.StringWriter;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.IdentityHashMap;
import java.util.Iterator;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.TimeZone;

@Mod(
    modid = "greggpt_me_export",
    name = "GregGPT ME Export",
    version = "1.0.0",
    acceptableRemoteVersions = "*"
)
public final class GregGPTMEExportMod {
  private static final long DEFAULT_INTERVAL_TICKS = 20L * 60L * 5L;
  private static long nextDumpTick = 0L;
  private static long tickCounter = 0L;

  public GregGPTMEExportMod() {
    FMLCommonHandler.instance().bus().register(this);
  }

  @SubscribeEvent
  public void onServerTick(TickEvent.ServerTickEvent event) {
    if (event.phase != TickEvent.Phase.END) {
      return;
    }
    tickCounter++;
    if (tickCounter < nextDumpTick) {
      return;
    }
    nextDumpTick = tickCounter + intervalTicks();
    try {
      Object server =
          invokeAny(
              FMLCommonHandler.instance(),
              new String[] {"getMinecraftServerInstance"},
              new Class[0],
              new Object[0]);
      writeDump(server);
    } catch (Throwable t) {
      System.out.println("[GREGGPT-ME] export failed: " + t);
      t.printStackTrace(System.out);
    }
  }

  private static long intervalTicks() {
    String raw = System.getProperty("greggpt.me_export_interval_seconds", "30");
    try {
      long seconds = Long.parseLong(raw);
      if (seconds < 30L) {
        seconds = 30L;
      }
      return seconds * 20L;
    } catch (NumberFormatException ignored) {
      return DEFAULT_INTERVAL_TICKS;
    }
  }

  private static void writeDump(Object server) throws Exception {
    writeMEDump(server);
    try {
      writeBlockInventoryDump(server);
    } catch (Throwable t) {
      System.out.println("[PICOCLAW-BLOCKINV] export failed: " + t);
      t.printStackTrace(System.out);
    }
  }

  private static void writeMEDump(Object server) throws Exception {
    if (server == null) {
      return;
    }
    File out = outputFile(server);
    File tmp = new File(out.getParentFile(), out.getName() + ".tmp");
    if (!out.getParentFile().exists() && !out.getParentFile().mkdirs()) {
      throw new IllegalStateException("failed to create " + out.getParentFile());
    }

    PrintWriter w = new PrintWriter(new OutputStreamWriter(new FileOutputStream(tmp), StandardCharsets.UTF_8));
    try {
      w.print("{\"generated_at\":\"");
      w.print(escape(nowUTC()));
      w.print("\",\"networks\":[");
      boolean firstNetwork = true;
      IdentityHashMap<Object, Boolean> seenGrids = new IdentityHashMap<Object, Boolean>();
      Object[] worlds = (Object[]) getField(server, "worldServers", "field_71305_c");
      for (Object world : worlds) {
        if (world == null) {
          continue;
        }
        Object loaded = getField(world, "loadedTileEntityList", "field_147482_g");
        if (!(loaded instanceof Iterable)) {
          continue;
        }
        for (Object te : (Iterable<?>) loaded) {
          Object grid = findGrid(te);
          if (grid == null || seenGrids.containsKey(grid)) {
            continue;
          }
          seenGrids.put(grid, Boolean.TRUE);
          Iterable<?> items = storageList(grid);
          if (items == null) {
            continue;
          }
          if (!firstNetwork) {
            w.print(',');
          }
          firstNetwork = false;
          writeNetwork(w, grid, te, items);
        }
      }
      w.print("]}");
    } finally {
      w.close();
    }
    if (!tmp.renameTo(out)) {
      throw new IllegalStateException("failed to rename " + tmp + " to " + out);
    }
    System.out.println("[GREGGPT-ME] wrote " + out.getAbsolutePath());
  }

  private static void writeBlockInventoryDump(Object server) throws Exception {
    if (server == null) {
      return;
    }
    File out = blockInventoryOutputFile(server);
    File tmp = new File(out.getParentFile(), out.getName() + ".tmp");
    if (!out.getParentFile().exists() && !out.getParentFile().mkdirs()) {
      throw new IllegalStateException("failed to create " + out.getParentFile());
    }

    int recordCount = 0;
    int itemCount = 0;
    PrintWriter w = new PrintWriter(new OutputStreamWriter(new FileOutputStream(tmp), StandardCharsets.UTF_8));
    try {
      w.print("{\"generated_at\":\"");
      w.print(escape(nowUTC()));
      w.print("\",\"inventories\":[");
      boolean firstRecord = true;
      Object[] worlds = (Object[]) getField(server, "worldServers", "field_71305_c");
      for (Object world : worlds) {
        if (world == null) {
          continue;
        }
        Object loaded = getField(world, "loadedTileEntityList", "field_147482_g");
        if (!(loaded instanceof Iterable)) {
          continue;
        }
        for (Object te : (Iterable<?>) loaded) {
          List<String> items = collectBlockInventoryItems(te);
          if (items.isEmpty() && !isInventoryTile(te)) {
            continue;
          }
          if (!firstRecord) {
            w.print(',');
          }
          firstRecord = false;
          writeBlockInventoryRecord(w, te, items);
          recordCount++;
          itemCount += items.size();
        }
      }
      w.print("]}");
    } finally {
      w.close();
    }
    if (!tmp.renameTo(out)) {
      throw new IllegalStateException("failed to rename " + tmp + " to " + out);
    }
    System.out.println("[PICOCLAW-BLOCKINV] wrote " + out.getAbsolutePath() + " count=" + recordCount + " items=" + itemCount);
  }

  private static File outputFile(Object server) throws Exception {
    String folder = String.valueOf(invokeAny(server, new String[] {"getFolderName", "func_71270_I"}, new Class[0], new Object[0]));
    File worldDir = (File) invokeAny(server, new String[] {"getFile", "func_71209_f"}, new Class[] {String.class}, new Object[] {folder});
    return new File(new File(worldDir, "greggpt"), "me_index.json");
  }

  private static File blockInventoryOutputFile(Object server) throws Exception {
    String folder = String.valueOf(invokeAny(server, new String[] {"getFolderName", "func_71270_I"}, new Class[0], new Object[0]));
    File worldDir = (File) invokeAny(server, new String[] {"getFile", "func_71209_f"}, new Class[] {String.class}, new Object[] {folder});
    return new File(new File(worldDir, "picoclaw"), "block_inventories.json");
  }

  private static void writeBlockInventoryRecord(PrintWriter w, Object te, List<String> itemRows) {
    int x = intField(te, "xCoord", "field_145851_c");
    int y = intField(te, "yCoord", "field_145848_d");
    int z = intField(te, "zCoord", "field_145849_e");
    int dim = dimensionID(te);
    Object world = invokeQuiet(te, new String[] {"getWorldObj", "func_145831_w"}, new Class[0], new Object[0]);
    Object block = blockAt(world, x, y, z);
    int meta = blockMetaAt(world, x, y, z);
    Object uid = uniqueIdentifierForBlock(block);
    String reg = "";
    if (uid != null) {
      reg = String.valueOf(getFieldQuiet(uid, "modId")) + ":" + String.valueOf(getFieldQuiet(uid, "name"));
    }
    String displayName = blockDisplayName(block);
    String source = inventorySource(te);

    w.print("{\"dim\":");
    w.print(dim);
    w.print(",\"x\":");
    w.print(x);
    w.print(",\"y\":");
    w.print(y);
    w.print(",\"z\":");
    w.print(z);
    w.print(",\"tile_class\":\"");
    w.print(escape(te.getClass().getName()));
    w.print("\",\"tile_id\":\"");
    w.print(escape(firstNonEmptyString(getFieldQuiet(te, "id"), invokeQuiet(te, new String[] {"getClassName"}, new Class[0], new Object[0]))));
    w.print("\",\"block_id\":");
    w.print(blockID(block));
    w.print(",\"block_meta\":");
    w.print(meta);
    w.print(",\"block_reg_name\":\"");
    w.print(escape(reg));
    w.print("\",\"block_display_name\":\"");
    w.print(escape(displayName));
    w.print("\",\"source\":\"");
    w.print(escape(source));
    w.print("\",\"items\":[");
    for (int i = 0; i < itemRows.size(); i++) {
      if (i > 0) {
        w.print(',');
      }
      w.print(itemRows.get(i));
    }
    w.print("]}");
  }

  private static List<String> collectBlockInventoryItems(Object te) {
    List<String> out = new ArrayList<String>();
    Set<String> seen = new LinkedHashSet<String>();

    try {
      Class<?> iinv = Class.forName("net.minecraft.inventory.IInventory");
      if (iinv.isInstance(te)) {
        int size = intInvoke(te, "getSizeInventory", "func_70302_i_");
        for (int slot = 0; slot < size; slot++) {
          Object stack = invokeQuiet(te, new String[] {"getStackInSlot", "func_70301_a"}, new Class[] {int.class}, new Object[] {Integer.valueOf(slot)});
          addStackJson(out, seen, stack, slot, inventorySource(te));
        }
      }
    } catch (Throwable ignored) {
    }

    addDirectGregTechStack(out, seen, te);
    collectStackContainer(out, seen, getFieldQuiet(te, "mInventory"), "field:mInventory");
    collectStackContainer(out, seen, getFieldQuiet(te, "mInventoryItems"), "field:mInventoryItems");
    collectStackContainer(out, seen, getFieldQuiet(te, "mInputItems"), "field:mInputItems");
    collectStackContainer(out, seen, getFieldQuiet(te, "mOutputItems"), "field:mOutputItems");
    collectStackContainer(out, seen, getFieldQuiet(te, "Inventory"), "field:Inventory");
    collectStackContainer(out, seen, getFieldQuiet(te, "Items"), "field:Items");
    collectStackContainer(out, seen, getFieldQuiet(te, "inventory"), "field:inventory");
    collectStackContainer(out, seen, getFieldQuiet(te, "contents"), "field:contents");
    return out;
  }

  private static boolean isInventoryTile(Object te) {
    try {
      Class<?> iinv = Class.forName("net.minecraft.inventory.IInventory");
      if (iinv.isInstance(te)) {
        return true;
      }
    } catch (Throwable ignored) {
    }
    return getFieldQuiet(te, "mInventory") != null
        || getFieldQuiet(te, "mInventoryItems") != null
        || getFieldQuiet(te, "mInputItems") != null
        || getFieldQuiet(te, "mOutputItems") != null
        || getFieldQuiet(te, "Inventory") != null
        || getFieldQuiet(te, "Items") != null
        || getFieldQuiet(te, "inventory") != null
        || getFieldQuiet(te, "contents") != null;
  }

  private static void addDirectGregTechStack(List<String> out, Set<String> seen, Object te) {
    Object stack = firstNonNull(
        getFieldQuiet(te, "mItemStack"),
        getFieldQuiet(te, "mStoredItemStack"),
        getFieldQuiet(te, "mStoredStack"),
        getFieldQuiet(te, "storedItem"));
    if (stack == null) {
      return;
    }
    long count = longField(te, "mItemCount", "mItemCountLong", "mItemAmount", "mStoredItemCount", "mStoredCount");
    if (count <= 0L) {
      count = intField(stack, "stackSize", "field_77994_a");
    }
    Object copy = copyStackWithCount(stack, count);
    addStackJson(out, seen, copy, -1, "gregtech-direct");
  }

  private static void collectStackContainer(List<String> out, Set<String> seen, Object container, String source) {
    if (container == null) {
      return;
    }
    Class<?> c = container.getClass();
    if (c.isArray()) {
      int len = java.lang.reflect.Array.getLength(container);
      for (int i = 0; i < len; i++) {
        addStackJson(out, seen, java.lang.reflect.Array.get(container, i), i, source);
      }
      return;
    }
    if (container instanceof Iterable) {
      int slot = 0;
      for (Object row : (Iterable<?>) container) {
        addStackJson(out, seen, row, slot, source);
        slot++;
      }
      return;
    }
    addStackJson(out, seen, container, -1, source);
  }

  private static void addStackJson(List<String> out, Set<String> seen, Object stack, int slot, String source) {
    if (!isPositiveItemStack(stack)) {
      return;
    }
    String row = itemJson(stack, slot, source);
    if (seen.add(row)) {
      out.add(row);
    }
  }

  private static Object findGrid(Object te) {
    try {
      Object node = null;
      try {
        Class<?> forgeDirection = Class.forName("net.minecraftforge.common.util.ForgeDirection");
        node = invokeAny(te, new String[] {"getGridNode"}, new Class[] {forgeDirection}, new Object[] {null});
      } catch (Throwable ignored) {
      }
      if (node == null) {
        node = invokeAny(te, new String[] {"getGridNode"}, new Class[0], new Object[0]);
      }
      if (node == null) {
        return null;
      }
      return invokeAny(node, new String[] {"getGrid"}, new Class[0], new Object[0]);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Iterable<?> storageList(Object grid) {
    try {
      Class<?> storageGridClass = Class.forName("appeng.api.networking.storage.IStorageGrid");
      Object storageGrid = invokeAny(grid, new String[] {"getCache"}, new Class[] {Class.class}, new Object[] {storageGridClass});
      if (storageGrid == null) {
        return null;
      }
      Object monitor = invokeAny(storageGrid, new String[] {"getItemInventory"}, new Class[0], new Object[0]);
      if (monitor == null) {
        return null;
      }
      Object list = invokeAny(monitor, new String[] {"getStorageList"}, new Class[0], new Object[0]);
      if (list instanceof Iterable) {
        return (Iterable<?>) list;
      }
      return null;
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static void writeNetwork(PrintWriter w, Object grid, Object te, Iterable<?> items) throws Exception {
    int x = intField(te, "xCoord", "field_145851_c");
    int y = intField(te, "yCoord", "field_145848_d");
    int z = intField(te, "zCoord", "field_145849_e");
    int dim = dimensionID(te);
    String label = "ME network " + x + "," + y + "," + z;
    w.print("{\"network_id\":\"");
    w.print(escape(Integer.toHexString(System.identityHashCode(grid))));
    w.print("\",\"label\":\"");
    w.print(escape(label));
    w.print("\",\"dim\":");
    w.print(dim);
    w.print(",\"x\":");
    w.print(x);
    w.print(",\"y\":");
    w.print(y);
    w.print(",\"z\":");
    w.print(z);
    StringBuilder itemJson = new StringBuilder();
    int entryCount = 0;
    int convertedCount = 0;
    int positiveCount = 0;
    String firstEntryClass = "";
    String firstStackClass = "";
    boolean first = true;
    Iterator<?> it = items.iterator();
    while (it.hasNext()) {
      Object entry = it.next();
      entryCount++;
      if (firstEntryClass.length() == 0 && entry != null) {
        firstEntryClass = entry.getClass().getName();
      }
      Object stack = toItemStack(entry);
      if (stack != null) {
        convertedCount++;
        if (firstStackClass.length() == 0) {
          firstStackClass = stack.getClass().getName();
        }
      }
      Object item = stack != null ? invokeAny(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]) : null;
      int count = stack != null ? intField(stack, "stackSize", "field_77994_a") : 0;
      if (stack == null || item == null || count <= 0) {
        continue;
      }
      positiveCount++;
      if (!first) {
        itemJson.append(',');
      }
      first = false;
      itemJson.append(itemJson(stack));
    }
    w.print(",\"entry_count\":");
    w.print(entryCount);
    w.print(",\"converted_count\":");
    w.print(convertedCount);
    w.print(",\"positive_count\":");
    w.print(positiveCount);
    w.print(",\"first_entry_class\":\"");
    w.print(escape(firstEntryClass));
    w.print("\",\"first_stack_class\":\"");
    w.print(escape(firstStackClass));
    w.print("\",\"items\":[");
    w.print(itemJson.toString());
    w.print("]}");
  }

  private static String itemJson(Object stack) {
    StringWriter out = new StringWriter();
    PrintWriter w = new PrintWriter(out);
    writeItem(w, stack);
    w.flush();
    return out.toString();
  }

  private static String itemJson(Object stack, int slot, String source) {
    StringWriter out = new StringWriter();
    PrintWriter w = new PrintWriter(out);
    writeItem(w, stack, slot, source);
    w.flush();
    return out.toString();
  }

  private static Object toItemStack(Object aeStack) {
    try {
      Object stack = invokeAny(aeStack, new String[] {"getItemStack"}, new Class[0], new Object[0]);
      if (stack != null) {
        Object copy = invokeAny(stack, new String[] {"copy", "func_77946_l"}, new Class[0], new Object[0]);
        Object size = invokeAny(aeStack, new String[] {"getStackSize"}, new Class[0], new Object[0]);
        if (size instanceof Number) {
          long n = ((Number) size).longValue();
          setField(copy, n > Integer.MAX_VALUE ? Integer.MAX_VALUE : (int) n, "stackSize", "field_77994_a");
        }
        return copy;
      }
    } catch (Throwable ignored) {
    }
    return null;
  }

  private static void writeItem(PrintWriter w, Object stack) {
    writeItem(w, stack, Integer.MIN_VALUE, "");
  }

  private static void writeItem(PrintWriter w, Object stack, int slot, String source) {
    Object item = invokeQuiet(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]);
    Object uid = uniqueIdentifier(item);
    String reg = "";
    if (uid != null) {
      reg = String.valueOf(getFieldQuiet(uid, "modId")) + ":" + String.valueOf(getFieldQuiet(uid, "name"));
    }
    w.print("{\"id\":");
    w.print(itemID(item));
    w.print(",\"damage\":");
    w.print(intInvoke(stack, "getItemDamage", "func_77960_j"));
    w.print(",\"count\":");
    w.print(intField(stack, "stackSize", "field_77994_a"));
    if (slot != Integer.MIN_VALUE) {
      w.print(",\"slot\":");
      w.print(slot);
    }
    if (source != null && source.length() > 0) {
      w.print(",\"source\":\"");
      w.print(escape(source));
      w.print("\"");
    }
    w.print(",\"reg_name\":\"");
    w.print(escape(reg));
    w.print("\",\"display_name\":\"");
    try {
      w.print(escape(String.valueOf(invokeAny(stack, new String[] {"getDisplayName", "func_82833_r"}, new Class[0], new Object[0]))));
    } catch (Throwable ignored) {
    }
    w.print("\",\"name\":\"");
    try {
      w.print(escape(String.valueOf(invokeAny(item, new String[] {"getUnlocalizedName", "func_77667_c"}, new Class[] {stack.getClass()}, new Object[] {stack}))));
    } catch (Throwable ignored) {
    }
    w.print("\"}");
  }

  private static boolean isPositiveItemStack(Object stack) {
    if (stack == null) {
      return false;
    }
    Object item = invokeQuiet(stack, new String[] {"getItem", "func_77973_b"}, new Class[0], new Object[0]);
    return item != null && intField(stack, "stackSize", "field_77994_a") > 0;
  }

  private static Object copyStackWithCount(Object stack, long count) {
    Object copy = invokeQuiet(stack, new String[] {"copy", "func_77946_l"}, new Class[0], new Object[0]);
    if (copy == null) {
      copy = stack;
    }
    if (count > Integer.MAX_VALUE) {
      count = Integer.MAX_VALUE;
    }
    try {
      setField(copy, Integer.valueOf((int) count), "stackSize", "field_77994_a");
    } catch (Throwable ignored) {
    }
    return copy;
  }

  private static Object invokeAny(Object target, String[] names, Class[] types, Object[] args) throws Exception {
    Class<?> c = target.getClass();
    while (c != null) {
      for (String name : names) {
        try {
          Method m = c.getMethod(name, types);
          m.setAccessible(true);
          return m.invoke(target, args);
        } catch (NoSuchMethodException ignored) {
        }
      }
      c = c.getSuperclass();
    }
    return null;
  }

  private static Object invokeQuiet(Object target, String[] names, Class[] types, Object[] args) {
    try {
      return invokeAny(target, names, types, args);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Object getField(Object target, String... names) throws Exception {
    Class<?> c = target.getClass();
    while (c != null) {
      for (String name : names) {
        try {
          Field f = c.getField(name);
          f.setAccessible(true);
          return f.get(target);
        } catch (NoSuchFieldException ignored) {
        }
      }
      c = c.getSuperclass();
    }
    return null;
  }

  private static Object getFieldQuiet(Object target, String... names) {
    try {
      return getField(target, names);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static void setField(Object target, Object value, String... names) throws Exception {
    Class<?> c = target.getClass();
    while (c != null) {
      for (String name : names) {
        try {
          Field f = c.getField(name);
          f.setAccessible(true);
          f.set(target, value);
          return;
        } catch (NoSuchFieldException ignored) {
        }
      }
      c = c.getSuperclass();
    }
  }

  private static int intField(Object target, String... names) {
    Object v = getFieldQuiet(target, names);
    return v instanceof Number ? ((Number) v).intValue() : 0;
  }

  private static long longField(Object target, String... names) {
    Object v = getFieldQuiet(target, names);
    return v instanceof Number ? ((Number) v).longValue() : 0L;
  }

  private static int intInvoke(Object target, String... names) {
    Object v = invokeQuiet(target, names, new Class[0], new Object[0]);
    return v instanceof Number ? ((Number) v).intValue() : 0;
  }

  private static int dimensionID(Object te) {
    try {
      Object world = invokeAny(te, new String[] {"getWorldObj", "func_145831_w"}, new Class[0], new Object[0]);
      Object provider = world != null ? getField(world, "provider", "field_73011_w") : null;
      return provider != null ? intField(provider, "dimensionId", "field_76574_g") : 0;
    } catch (Throwable ignored) {
      return 0;
    }
  }

  private static Object uniqueIdentifier(Object item) {
    try {
      Class<?> itemClass = Class.forName("net.minecraft.item.Item");
      Class<?> registry = Class.forName("cpw.mods.fml.common.registry.GameRegistry");
      Method m = registry.getMethod("findUniqueIdentifierFor", itemClass);
      return m.invoke(null, item);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Object uniqueIdentifierForBlock(Object block) {
    try {
      Class<?> blockClass = Class.forName("net.minecraft.block.Block");
      Class<?> registry = Class.forName("cpw.mods.fml.common.registry.GameRegistry");
      Method m = registry.getMethod("findUniqueIdentifierFor", blockClass);
      return m.invoke(null, block);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static int itemID(Object item) {
    try {
      Class<?> itemClass = Class.forName("net.minecraft.item.Item");
      Method m;
      try {
        m = itemClass.getMethod("getIdFromItem", itemClass);
      } catch (NoSuchMethodException ignored) {
        m = itemClass.getMethod("func_150891_b", itemClass);
      }
      Object v = m.invoke(null, item);
      return v instanceof Number ? ((Number) v).intValue() : 0;
    } catch (Throwable ignored) {
      return 0;
    }
  }

  private static Object blockAt(Object world, int x, int y, int z) {
    if (world == null) {
      return null;
    }
    return invokeQuiet(world, new String[] {"getBlock", "func_147439_a"}, new Class[] {int.class, int.class, int.class}, new Object[] {Integer.valueOf(x), Integer.valueOf(y), Integer.valueOf(z)});
  }

  private static int blockMetaAt(Object world, int x, int y, int z) {
    Object v = world == null ? null : invokeQuiet(world, new String[] {"getBlockMetadata", "func_72805_g"}, new Class[] {int.class, int.class, int.class}, new Object[] {Integer.valueOf(x), Integer.valueOf(y), Integer.valueOf(z)});
    return v instanceof Number ? ((Number) v).intValue() : 0;
  }

  private static int blockID(Object block) {
    if (block == null) {
      return 0;
    }
    try {
      Class<?> blockClass = Class.forName("net.minecraft.block.Block");
      Method m;
      try {
        m = blockClass.getMethod("getIdFromBlock", blockClass);
      } catch (NoSuchMethodException ignored) {
        m = blockClass.getMethod("func_149682_b", blockClass);
      }
      Object v = m.invoke(null, block);
      return v instanceof Number ? ((Number) v).intValue() : 0;
    } catch (Throwable ignored) {
      return 0;
    }
  }

  private static String blockDisplayName(Object block) {
    if (block == null) {
      return "";
    }
    Object v = invokeQuiet(block, new String[] {"getLocalizedName", "func_149732_F"}, new Class[0], new Object[0]);
    if (v != null && String.valueOf(v).length() > 0) {
      return String.valueOf(v);
    }
    v = invokeQuiet(block, new String[] {"getUnlocalizedName", "func_149739_a"}, new Class[0], new Object[0]);
    return v != null ? String.valueOf(v) : "";
  }

  private static String inventorySource(Object te) {
    try {
      Class<?> sided = Class.forName("net.minecraft.inventory.ISidedInventory");
      if (sided.isInstance(te)) {
        return "ISidedInventory";
      }
    } catch (Throwable ignored) {
    }
    try {
      Class<?> inv = Class.forName("net.minecraft.inventory.IInventory");
      if (inv.isInstance(te)) {
        return "IInventory";
      }
    } catch (Throwable ignored) {
    }
    return "tile-fields";
  }

  private static Object firstNonNull(Object... values) {
    for (Object v : values) {
      if (v != null) {
        return v;
      }
    }
    return null;
  }

  private static String firstNonEmptyString(Object... values) {
    for (Object v : values) {
      if (v == null) {
        continue;
      }
      String s = String.valueOf(v).trim();
      if (s.length() > 0 && !"<nil>".equals(s)) {
        return s;
      }
    }
    return "";
  }

  private static String nowUTC() {
    SimpleDateFormat fmt = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'");
    fmt.setTimeZone(TimeZone.getTimeZone("UTC"));
    return fmt.format(new Date());
  }

  private static String escape(String value) {
    if (value == null) {
      return "";
    }
    StringBuilder b = new StringBuilder(value.length() + 8);
    for (int i = 0; i < value.length(); i++) {
      char ch = value.charAt(i);
      switch (ch) {
        case '\\':
          b.append("\\\\");
          break;
        case '"':
          b.append("\\\"");
          break;
        case '\n':
        case '\r':
        case '\t':
          b.append(' ');
          break;
        default:
          b.append(ch);
      }
    }
    return b.toString().trim();
  }
}
