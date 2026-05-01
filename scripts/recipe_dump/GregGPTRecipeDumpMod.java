package greggpt.recipedump;

import cpw.mods.fml.common.Loader;
import cpw.mods.fml.common.LoaderState;
import cpw.mods.fml.common.Mod;
import cpw.mods.fml.common.Mod.EventHandler;
import cpw.mods.fml.common.event.FMLLoadCompleteEvent;
import cpw.mods.fml.common.registry.GameRegistry;
import java.io.BufferedWriter;
import java.io.File;
import java.io.IOException;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.net.URL;
import java.net.URLClassLoader;
import java.nio.charset.StandardCharsets;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.IdentityHashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;

@Mod(
    modid = "greggpt_recipe_dump",
    name = "GregGPT Recipe Dump",
    version = "1.0.0",
    acceptableRemoteVersions = "*"
)
public final class GregGPTRecipeDumpMod {
  private static final AtomicBoolean WORKER_STARTED = new AtomicBoolean(false);
  private static final String DUMP_FILE_NAME = "greggpt_recipes.sqlite";
  private static final String LOG_FILE_NAME = "greggpt_recipe_dump.log";

  public GregGPTRecipeDumpMod() {
    startDumpWorker("constructor");
  }

  @EventHandler
  public void onLoadComplete(FMLLoadCompleteEvent event) {
    startDumpWorker("load-complete");
  }

  private static void startDumpWorker(final String trigger) {
    if (!WORKER_STARTED.compareAndSet(false, true)) {
      appendLog("[GREGGPT] recipe dump worker already started; ignoring trigger " + trigger);
      return;
    }

    Thread worker =
        new Thread(
            new Runnable() {
              @Override
              public void run() {
                runDumpWorker(trigger);
              }
            },
            "greggpt-recipe-dump");
    worker.setDaemon(true);
    worker.start();
  }

  private static void runDumpWorker(String trigger) {
    File outFile = new File(getDumpsDir(), DUMP_FILE_NAME);
    appendLog("[GREGGPT] recipe dump worker started from " + trigger);

    for (int attempt = 1; attempt <= 300; attempt++) {
      Loader loader = Loader.instance();
      LoaderState state = loader != null ? loader.getLoaderState() : null;
      boolean available = loader != null && loader.hasReachedState(LoaderState.AVAILABLE);
      appendLog(
          "[GREGGPT] recipe dump attempt "
              + attempt
              + " state="
              + (state != null ? state.name() : "null"));

      if (available) {
        try {
          writeDump(outFile);
          appendLog("[GREGGPT] wrote recipe dump to " + outFile.getAbsolutePath());
          System.out.println("[GREGGPT] wrote recipe dump to " + outFile.getAbsolutePath());
          sleepQuietly(1500L);
          Runtime.getRuntime().halt(0);
          return;
        } catch (Throwable t) {
          appendThrowable("[GREGGPT] failed to write recipe dump", t);
          throw new RuntimeException("failed to write recipe dump", t);
        }
      }

      sleepQuietly(1000L);
    }

    throw new RuntimeException("timed out waiting for Forge to become available");
  }

  private static void writeDump(File outFile) throws Exception {
    loadSqliteDriver();
    if (outFile.exists() && !outFile.delete()) {
      throw new IOException("failed to replace existing dump: " + outFile);
    }

    Connection conn = DriverManager.getConnection("jdbc:sqlite:" + outFile.getAbsolutePath());
    try {
      configureConnection(conn);
      conn.setAutoCommit(false);
      Db db = new Db(conn);
      db.createSchema();
      db.putManifest("schema_version", "1");
      db.putManifest("mod_version", "1.0.0");
      db.putManifest("minecraft_version", "1.7.10");
      db.putManifest("dump_file", outFile.getAbsolutePath());
      db.putManifest("started_at_millis", String.valueOf(System.currentTimeMillis()));

      int vanillaCount = new VanillaDumper(db).dump();
      int gregTechCount = new GregTechDumper(db).dump();

      db.putManifest("vanilla_recipe_count", String.valueOf(vanillaCount));
      db.putManifest("gregtech_recipe_count", String.valueOf(gregTechCount));
      db.putManifest("finished_at_millis", String.valueOf(System.currentTimeMillis()));
      conn.commit();
    } catch (Throwable t) {
      rollbackQuietly(conn);
      throw t;
    } finally {
      conn.close();
    }
  }

  private static void configureConnection(Connection conn) throws SQLException {
    Statement st = conn.createStatement();
    try {
      st.executeUpdate("PRAGMA journal_mode=OFF");
      st.executeUpdate("PRAGMA synchronous=OFF");
    } finally {
      st.close();
    }
  }

  private static void loadSqliteDriver() throws Exception {
    try {
      Class.forName("org.sqlite.JDBC");
      return;
    } catch (ClassNotFoundException ignored) {
    }

    String jarPath = System.getProperty("greggpt.sqliteJdbcJar");
    if (jarPath == null || jarPath.trim().isEmpty()) {
      jarPath = System.getenv("GREGGPT_SQLITE_JDBC_JAR");
    }
    if (jarPath == null || jarPath.trim().isEmpty()) {
      File jar = findBundledSqliteJdbcJar();
      if (jar != null) {
        jarPath = jar.getAbsolutePath();
      }
    }
    if (jarPath != null && !jarPath.trim().isEmpty()) {
      File jar = new File(jarPath.trim());
      URL url = jar.toURI().toURL();
      ClassLoader parent = Thread.currentThread().getContextClassLoader();
      URLClassLoader loader = new URLClassLoader(new URL[] {url}, parent);
      Class.forName("org.sqlite.JDBC", true, loader);
      return;
    }

    Class.forName("org.sqlite.JDBC");
  }

  private static File findBundledSqliteJdbcJar() {
    File baseDir = getDumpsDir().getParentFile();
    File modsDir = baseDir == null ? null : new File(baseDir, "mods");
    File[] candidates = modsDir == null ? null : modsDir.listFiles();
    if (candidates == null) {
      return null;
    }
    for (int i = 0; i < candidates.length; i++) {
      String name = candidates[i].getName().toLowerCase(Locale.ROOT);
      if (candidates[i].isFile() && name.startsWith("sqlite-jdbc") && name.endsWith(".jar")) {
        return candidates[i];
      }
    }
    return null;
  }

  private static final class VanillaDumper {
    private final Db db;
    private final Accessors accessors = new Accessors();

    private VanillaDumper(Db db) {
      this.db = db;
    }

    private int dump() {
      int count = 0;
      count += dumpCrafting();
      count += dumpFurnace();
      return count;
    }

    private int dumpCrafting() {
      int count = 0;
      try {
        Object craftingManager = invokeStatic(classForName("net.minecraft.item.crafting.CraftingManager"), "getInstance");
        Object recipes = invokeAny(craftingManager, "getRecipeList", "func_77592_b");
        for (Object recipe : iterable(recipes)) {
          if (recipe == null) {
            continue;
          }
          Object output = invokeAny(recipe, "getRecipeOutput", "func_77571_b");
          if (isEmptyStack(output)) {
            continue;
          }
          String handlerName = recipe.getClass().getName();
          int recipeId = db.insertRecipe(handlerName, handlerName + "#" + count, "crafting", null, null, null, false);
          db.insertMetadata(recipeId, "class", recipe.getClass().getName());
          dumpCraftingInputs(recipeId, recipe);
          db.insertOutput(recipeId, 0, "item", accessors.stackSize(output), db.itemId(output), null, null, null);
          count++;
        }
      } catch (Throwable t) {
        appendThrowable("[GREGGPT] vanilla crafting dump failed", t);
      }
      return count;
    }

    private void dumpCraftingInputs(int recipeId, Object recipe) throws Exception {
      Object inputs = firstFieldValue(recipe, "recipeItems", "field_77574_d", "input", "items");
      if (inputs == null) {
        inputs = firstFieldValue(recipe, "recipeItems", "field_77579_b");
      }
      int pos = 0;
      for (Object input : iterable(inputs)) {
        dumpInputObject(recipeId, pos, input);
        pos++;
      }
    }

    private int dumpFurnace() {
      int count = 0;
      try {
        Object furnace = invokeStatic(classForName("net.minecraft.item.crafting.FurnaceRecipes"), "smelting", "func_77602_a");
        Object map = invokeAny(furnace, "getSmeltingList", "func_77599_b");
        if (!(map instanceof Map)) {
          return 0;
        }
        int position = 0;
        for (Object entryObj : ((Map<?, ?>) map).entrySet()) {
          Map.Entry<?, ?> entry = (Map.Entry<?, ?>) entryObj;
          Object input = entry.getKey();
          Object output = entry.getValue();
          if (isEmptyStack(input) || isEmptyStack(output)) {
            continue;
          }
          int recipeId = db.insertRecipe("minecraft.furnace", "minecraft.furnace#" + count, "furnace", null, null, null, false);
          dumpInputObject(recipeId, 0, input);
          db.insertOutput(recipeId, position, "item", accessors.stackSize(output), db.itemId(output), null, null, null);
          count++;
          position++;
        }
      } catch (Throwable t) {
        appendThrowable("[GREGGPT] vanilla furnace dump failed", t);
      }
      return count;
    }

    private void dumpInputObject(int recipeId, int position, Object input) throws Exception {
      if (input == null || isEmptyStack(input)) {
        return;
      }
      if (isItemStack(input)) {
        int inputId = db.insertInput(recipeId, position, "item", accessors.stackSize(input), null);
        db.insertInputOption(inputId, 0, "item", db.itemId(input), null, null, null, null, accessors.stackSize(input));
        return;
      }
      if (input instanceof String) {
        db.insertInput(recipeId, position, "oredict", 1, input.toString());
        return;
      }
      if (input instanceof Collection || input.getClass().isArray()) {
        List<Object> options = iterable(input);
        if (options.isEmpty()) {
          return;
        }
        int inputId = db.insertInput(recipeId, position, "item", 1, null);
        int ordinal = 0;
        for (Object option : options) {
          if (isItemStack(option) && !isEmptyStack(option)) {
            db.insertInputOption(inputId, ordinal, "item", db.itemId(option), null, null, null, null, accessors.stackSize(option));
            ordinal++;
          } else if (option instanceof String) {
            db.insertInputOption(inputId, ordinal, "oredict", null, null, null, option.toString(), null, 1);
            ordinal++;
          }
        }
        return;
      }
      db.insertInput(recipeId, position, "unknown", 1, input.getClass().getName());
    }
  }

  private static final class GregTechDumper {
    private final Db db;
    private final Accessors accessors = new Accessors();

    private GregTechDumper(Db db) {
      this.db = db;
    }

    private int dump() {
      List<Object> maps = discoverRecipeMaps();
      int count = 0;
      for (Object recipeMap : maps) {
        String handlerName = recipeMapName(recipeMap);
        Collection<?> recipes = new ArrayList<Object>(recipeList(recipeMap));
        int ordinal = 0;
        for (Object recipe : recipes) {
          if (recipe == null) {
            continue;
          }
          try {
            String key = handlerName + "#" + ordinal;
            Integer duration = intField(recipe, "mDuration");
            Integer eut = intField(recipe, "mEUt");
            Integer special = intField(recipe, "mSpecialValue");
            Boolean hidden = boolField(recipe, "mHidden", "mFakeRecipe");
            int recipeId = db.insertRecipe(handlerName, key, "gregtech", duration, eut, special, hidden != null && hidden.booleanValue());
            db.insertMetadata(recipeId, "class", recipe.getClass().getName());
            db.insertMetadata(recipeId, "map_class", recipeMap.getClass().getName());
            dumpItems(recipeId, fieldValue(recipe, "mInputs"), true);
            dumpFluids(recipeId, fieldValue(recipe, "mFluidInputs"), true);
            dumpItems(recipeId, fieldValue(recipe, "mOutputs"), false);
            dumpFluids(recipeId, fieldValue(recipe, "mFluidOutputs"), false);
            dumpChances(recipeId, fieldValue(recipe, "mChances"));
            count++;
          } catch (Throwable t) {
            appendThrowable("[GREGGPT] failed to dump GregTech recipe from " + handlerName, t);
          }
          ordinal++;
        }
      }
      return count;
    }

    private void dumpItems(int recipeId, Object stacks, boolean input) throws Exception {
      int pos = 0;
      for (Object stack : iterable(stacks)) {
        if (isEmptyStack(stack)) {
          pos++;
          continue;
        }
        if (input) {
          int inputId = db.insertInput(recipeId, pos, "item", accessors.stackSize(stack), null);
          db.insertInputOption(inputId, 0, "item", db.itemId(stack), null, null, null, null, accessors.stackSize(stack));
        } else {
          db.insertOutput(recipeId, pos, "item", accessors.stackSize(stack), db.itemId(stack), null, null, null);
        }
        pos++;
      }
    }

    private void dumpFluids(int recipeId, Object fluids, boolean input) throws Exception {
      int pos = 0;
      for (Object fluidStack : iterable(fluids)) {
        if (fluidStack == null) {
          pos++;
          continue;
        }
        int amount = fluidAmount(fluidStack);
        int fluidId = db.fluidId(fluidStack);
        if (input) {
          int inputId = db.insertInput(recipeId, pos, "fluid", amount, null);
          db.insertInputOption(inputId, 0, "fluid", null, fluidId, null, null, null, amount);
        } else {
          db.insertOutput(recipeId, pos, "fluid", amount, null, fluidId, null, null);
        }
        pos++;
      }
    }

    private void dumpChances(int recipeId, Object chances) throws SQLException {
      int pos = 0;
      for (Object chance : iterable(chances)) {
        if (chance != null) {
          db.insertMetadata(recipeId, "output_chance_" + pos, chance.toString());
        }
        pos++;
      }
    }

    private static List<Object> discoverRecipeMaps() {
      IdentityHashMap<Object, Boolean> seen = new IdentityHashMap<Object, Boolean>();
      List<Object> maps = new ArrayList<Object>();
      inspectRecipeMapHolder("gregtech.api.recipe.RecipeMaps", seen, maps);
      inspectRecipeMapHolder("gregtech.api.enums.GT_Values", seen, maps);
      inspectRecipeMapHolder("gregtech.api.util.GT_Recipe$GT_Recipe_Map", seen, maps);
      Collections.sort(
          maps,
          new Comparator<Object>() {
            @Override
            public int compare(Object left, Object right) {
              return recipeMapName(left).compareTo(recipeMapName(right));
            }
          });
      appendLog("[GREGGPT] discovered " + maps.size() + " GregTech recipe maps");
      return maps;
    }

    private static void inspectRecipeMapHolder(String className, IdentityHashMap<Object, Boolean> seen, List<Object> maps) {
      Class<?> type;
      try {
        type = classForName(className);
      } catch (Throwable ignored) {
        return;
      }
      Field[] fields = type.getDeclaredFields();
      for (int i = 0; i < fields.length; i++) {
        Field field = fields[i];
        if ((field.getModifiers() & java.lang.reflect.Modifier.STATIC) == 0) {
          continue;
        }
        try {
          field.setAccessible(true);
          Object value = field.get(null);
          inspectRecipeMapValue(value, seen, maps);
        } catch (Throwable ignored) {
        }
      }
    }

    private static void inspectRecipeMapValue(Object value, IdentityHashMap<Object, Boolean> seen, List<Object> maps) {
      if (value == null) {
        return;
      }
      if (seen.containsKey(value)) {
        return;
      }
      Collection<?> recipes = recipeList(value);
      if (recipes != null) {
        seen.put(value, Boolean.TRUE);
        maps.add(value);
        return;
      }
      if (value instanceof Map) {
        for (Object child : ((Map<?, ?>) value).values()) {
          inspectRecipeMapValue(child, seen, maps);
        }
      } else if (value instanceof Collection || value.getClass().isArray()) {
        for (Object child : iterable(value)) {
          inspectRecipeMapValue(child, seen, maps);
        }
      }
    }

    private static Collection<?> recipeList(Object recipeMap) {
      Object list = fieldValue(recipeMap, "mRecipeList");
      if (list == null) {
        list = fieldValue(recipeMap, "mRecipeListBackend");
      }
      if (list == null) {
        try {
          list = invokeAny(recipeMap, "getAllRecipes");
        } catch (Throwable ignored) {
        }
      }
      if (list instanceof Collection) {
        return (Collection<?>) list;
      }
      if (list instanceof Map) {
        return ((Map<?, ?>) list).values();
      }
      return null;
    }

    private static String recipeMapName(Object recipeMap) {
      String[] fields = {"mUnlocalizedName", "mName", "mNEIName", "unlocalizedName", "name"};
      for (int i = 0; i < fields.length; i++) {
        Object value = fieldValue(recipeMap, fields[i]);
        if (value != null && !value.toString().trim().isEmpty()) {
          return value.toString();
        }
      }
      return recipeMap.getClass().getName() + "@" + Integer.toHexString(System.identityHashCode(recipeMap));
    }
  }

  private static final class Db {
    private final Connection conn;
    private final Map<String, Integer> itemIds = new LinkedHashMap<String, Integer>();
    private final Map<String, Integer> fluidIds = new LinkedHashMap<String, Integer>();
    private final Map<String, Integer> handlerIds = new LinkedHashMap<String, Integer>();
    private final Accessors accessors = new Accessors();

    private PreparedStatement putManifest;
    private PreparedStatement insertItem;
    private PreparedStatement selectItem;
    private PreparedStatement insertFluid;
    private PreparedStatement selectFluid;
    private PreparedStatement insertHandler;
    private PreparedStatement selectHandler;
    private PreparedStatement insertRecipe;
    private PreparedStatement insertInput;
    private PreparedStatement insertInputOption;
    private PreparedStatement insertOutput;
    private PreparedStatement insertMetadata;

    private Db(Connection conn) {
      this.conn = conn;
    }

    private void createSchema() throws SQLException {
      Statement st = conn.createStatement();
      try {
        st.executeUpdate("CREATE TABLE manifest (key TEXT PRIMARY KEY, value TEXT NOT NULL)");
        st.executeUpdate(
            "CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, registry_name TEXT NOT NULL, damage INTEGER NOT NULL, display_name TEXT, unlocalized_name TEXT, max_damage INTEGER, UNIQUE(registry_name, damage))");
        st.executeUpdate(
            "CREATE TABLE fluids (id INTEGER PRIMARY KEY AUTOINCREMENT, fluid_name TEXT NOT NULL UNIQUE, localized_name TEXT)");
        st.executeUpdate(
            "CREATE TABLE ore_dict_entries (ore_name TEXT NOT NULL, item_id INTEGER NOT NULL, PRIMARY KEY(ore_name, item_id), FOREIGN KEY(item_id) REFERENCES items(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_handlers (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)");
        st.executeUpdate(
            "CREATE TABLE recipes (id INTEGER PRIMARY KEY AUTOINCREMENT, handler_id INTEGER NOT NULL, recipe_key TEXT NOT NULL, category TEXT NOT NULL, duration_ticks INTEGER, eut INTEGER, special_value INTEGER, hidden INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(handler_id) REFERENCES recipe_handlers(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_inputs (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, position INTEGER NOT NULL, kind TEXT NOT NULL, amount INTEGER, label TEXT, FOREIGN KEY(recipe_id) REFERENCES recipes(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_input_options (id INTEGER PRIMARY KEY AUTOINCREMENT, input_id INTEGER NOT NULL, option_index INTEGER NOT NULL, kind TEXT NOT NULL, item_id INTEGER, fluid_id INTEGER, amount INTEGER, ore_name TEXT, label TEXT, FOREIGN KEY(input_id) REFERENCES recipe_inputs(id), FOREIGN KEY(item_id) REFERENCES items(id), FOREIGN KEY(fluid_id) REFERENCES fluids(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_outputs (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, position INTEGER NOT NULL, kind TEXT NOT NULL, amount INTEGER, item_id INTEGER, fluid_id INTEGER, chance INTEGER, label TEXT, FOREIGN KEY(recipe_id) REFERENCES recipes(id), FOREIGN KEY(item_id) REFERENCES items(id), FOREIGN KEY(fluid_id) REFERENCES fluids(id))");
        st.executeUpdate(
            "CREATE TABLE recipe_metadata (id INTEGER PRIMARY KEY AUTOINCREMENT, recipe_id INTEGER NOT NULL, key TEXT NOT NULL, value TEXT, FOREIGN KEY(recipe_id) REFERENCES recipes(id))");
        st.executeUpdate("CREATE INDEX idx_items_registry_damage ON items(registry_name, damage)");
        st.executeUpdate("CREATE INDEX idx_recipes_handler ON recipes(handler_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_inputs_recipe ON recipe_inputs(recipe_id)");
        st.executeUpdate("CREATE INDEX idx_recipe_outputs_recipe ON recipe_outputs(recipe_id)");
      } finally {
        st.close();
      }

      putManifest = conn.prepareStatement("INSERT OR REPLACE INTO manifest(key, value) VALUES (?, ?)");
      insertItem =
          conn.prepareStatement(
              "INSERT OR IGNORE INTO items(registry_name, damage, display_name, unlocalized_name, max_damage) VALUES (?, ?, ?, ?, ?)");
      selectItem = conn.prepareStatement("SELECT id FROM items WHERE registry_name = ? AND damage = ?");
      insertFluid = conn.prepareStatement("INSERT OR IGNORE INTO fluids(fluid_name, localized_name) VALUES (?, ?)");
      selectFluid = conn.prepareStatement("SELECT id FROM fluids WHERE fluid_name = ?");
      insertHandler = conn.prepareStatement("INSERT OR IGNORE INTO recipe_handlers(name) VALUES (?)");
      selectHandler = conn.prepareStatement("SELECT id FROM recipe_handlers WHERE name = ?");
      insertRecipe =
          conn.prepareStatement(
              "INSERT INTO recipes(handler_id, recipe_key, category, duration_ticks, eut, special_value, hidden) VALUES (?, ?, ?, ?, ?, ?, ?)",
              Statement.RETURN_GENERATED_KEYS);
      insertInput =
          conn.prepareStatement(
              "INSERT INTO recipe_inputs(recipe_id, position, kind, amount, label) VALUES (?, ?, ?, ?, ?)",
              Statement.RETURN_GENERATED_KEYS);
      insertInputOption =
          conn.prepareStatement(
              "INSERT INTO recipe_input_options(input_id, option_index, kind, item_id, fluid_id, amount, ore_name, label) VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
      insertOutput =
          conn.prepareStatement(
              "INSERT INTO recipe_outputs(recipe_id, position, kind, amount, item_id, fluid_id, chance, label) VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
      insertMetadata = conn.prepareStatement("INSERT INTO recipe_metadata(recipe_id, key, value) VALUES (?, ?, ?)");
    }

    private void putManifest(String key, String value) throws SQLException {
      putManifest.setString(1, key);
      putManifest.setString(2, value);
      putManifest.executeUpdate();
    }

    private int itemId(Object stack) throws Exception {
      String registryName = accessors.registryName(stack);
      int damage = accessors.damage(stack);
      String key = registryName + "#" + damage;
      Integer cached = itemIds.get(key);
      if (cached != null) {
        return cached.intValue();
      }

      insertItem.setString(1, registryName);
      insertItem.setInt(2, damage);
      insertItem.setString(3, accessors.displayName(stack));
      insertItem.setString(4, accessors.unlocalizedName(stack));
      setNullableInt(insertItem, 5, accessors.maxDamage(stack));
      insertItem.executeUpdate();

      selectItem.setString(1, registryName);
      selectItem.setInt(2, damage);
      int id = selectSingleId(selectItem, "item " + key);
      itemIds.put(key, Integer.valueOf(id));
      return id;
    }

    private int fluidId(Object fluidStack) throws Exception {
      String name = fluidName(fluidStack);
      Integer cached = fluidIds.get(name);
      if (cached != null) {
        return cached.intValue();
      }
      insertFluid.setString(1, name);
      insertFluid.setString(2, fluidLocalizedName(fluidStack));
      insertFluid.executeUpdate();

      selectFluid.setString(1, name);
      int id = selectSingleId(selectFluid, "fluid " + name);
      fluidIds.put(name, Integer.valueOf(id));
      return id;
    }

    private int handlerId(String name) throws SQLException {
      Integer cached = handlerIds.get(name);
      if (cached != null) {
        return cached.intValue();
      }
      insertHandler.setString(1, name);
      insertHandler.executeUpdate();
      selectHandler.setString(1, name);
      int id = selectSingleId(selectHandler, "recipe handler " + name);
      handlerIds.put(name, Integer.valueOf(id));
      return id;
    }

    private int insertRecipe(
        String handlerName,
        String recipeKey,
        String category,
        Integer durationTicks,
        Integer eut,
        Integer specialValue,
        boolean hidden)
        throws SQLException {
      insertRecipe.setInt(1, handlerId(handlerName));
      insertRecipe.setString(2, recipeKey);
      insertRecipe.setString(3, category);
      setNullableInt(insertRecipe, 4, durationTicks);
      setNullableInt(insertRecipe, 5, eut);
      setNullableInt(insertRecipe, 6, specialValue);
      insertRecipe.setInt(7, hidden ? 1 : 0);
      insertRecipe.executeUpdate();
      return generatedId(insertRecipe);
    }

    private int insertInput(int recipeId, int position, String kind, Integer amount, String label) throws SQLException {
      insertInput.setInt(1, recipeId);
      insertInput.setInt(2, position);
      insertInput.setString(3, kind);
      setNullableInt(insertInput, 4, amount);
      insertInput.setString(5, label);
      insertInput.executeUpdate();
      return generatedId(insertInput);
    }

    private void insertInputOption(
        int inputId,
        int optionIndex,
        String kind,
        Integer itemId,
        Integer fluidId,
        Integer chance,
        String oreName,
        String label,
        Integer amount)
        throws SQLException {
      insertInputOption.setInt(1, inputId);
      insertInputOption.setInt(2, optionIndex);
      insertInputOption.setString(3, kind);
      setNullableInt(insertInputOption, 4, itemId);
      setNullableInt(insertInputOption, 5, fluidId);
      setNullableInt(insertInputOption, 6, amount);
      insertInputOption.setString(7, oreName);
      insertInputOption.setString(8, label);
      insertInputOption.executeUpdate();
    }

    private void insertOutput(
        int recipeId,
        int position,
        String kind,
        Integer amount,
        Integer itemId,
        Integer fluidId,
        Integer chance,
        String label)
        throws SQLException {
      insertOutput.setInt(1, recipeId);
      insertOutput.setInt(2, position);
      insertOutput.setString(3, kind);
      setNullableInt(insertOutput, 4, amount);
      setNullableInt(insertOutput, 5, itemId);
      setNullableInt(insertOutput, 6, fluidId);
      setNullableInt(insertOutput, 7, chance);
      insertOutput.setString(8, label);
      insertOutput.executeUpdate();
    }

    private void insertMetadata(int recipeId, String key, String value) throws SQLException {
      insertMetadata.setInt(1, recipeId);
      insertMetadata.setString(2, key);
      insertMetadata.setString(3, value);
      insertMetadata.executeUpdate();
    }
  }

  private static final class Accessors {
    private Method stackGetItem;
    private Method stackGetItemDamage;
    private Method stackGetDisplayName;
    private Method stackGetUnlocalizedName;
    private Method stackGetMaxDamage;
    private Method uniqueIdentifierLookup;
    private Field uniqueIdentifierModId;
    private Field uniqueIdentifierName;
    private Field stackSizeField;

    private int stackSize(Object stack) {
      try {
        if (stackSizeField == null) {
          stackSizeField = findField(stack.getClass(), "stackSize", "field_77994_a");
        }
        return stackSizeField.getInt(stack);
      } catch (Throwable ignored) {
        return 1;
      }
    }

    private int damage(Object stack) {
      try {
        if (stackGetItemDamage == null) {
          stackGetItemDamage = findMethod(stack.getClass(), "getItemDamage", "func_77960_j");
        }
        return ((Integer) stackGetItemDamage.invoke(stack)).intValue();
      } catch (Exception e) {
        return 0;
      }
    }

    private String displayName(Object stack) {
      try {
        if (stackGetDisplayName == null) {
          stackGetDisplayName = findMethod(stack.getClass(), "getDisplayName", "func_82833_r");
        }
        Object value = stackGetDisplayName.invoke(stack);
        return value != null ? value.toString() : "";
      } catch (Throwable ignored) {
        return "";
      }
    }

    private String unlocalizedName(Object stack) {
      try {
        if (stackGetUnlocalizedName == null) {
          stackGetUnlocalizedName = findMethod(stack.getClass(), "getUnlocalizedName", "func_77977_a");
        }
        Object value = stackGetUnlocalizedName.invoke(stack);
        return value != null ? value.toString() : "";
      } catch (Throwable ignored) {
        return "";
      }
    }

    private Integer maxDamage(Object stack) {
      try {
        if (stackGetMaxDamage == null) {
          stackGetMaxDamage = findMethod(stack.getClass(), "getMaxDamage", "func_77958_k");
        }
        return (Integer) stackGetMaxDamage.invoke(stack);
      } catch (Throwable ignored) {
        return null;
      }
    }

    private String registryName(Object stack) throws Exception {
      Object item = item(stack);
      if (item == null) {
        return "";
      }
      if (uniqueIdentifierLookup == null) {
        uniqueIdentifierLookup = findMethod(GameRegistry.class, "findUniqueIdentifierFor", item.getClass());
      }
      Object identifier = uniqueIdentifierLookup.invoke(null, item);
      if (identifier == null) {
        return "";
      }
      if (uniqueIdentifierModId == null) {
        uniqueIdentifierModId = findField(identifier.getClass(), "modId");
      }
      if (uniqueIdentifierName == null) {
        uniqueIdentifierName = findField(identifier.getClass(), "name");
      }
      Object modId = uniqueIdentifierModId.get(identifier);
      Object name = uniqueIdentifierName.get(identifier);
      if (modId == null || name == null) {
        return "";
      }
      return modId.toString() + ":" + name.toString();
    }

    private Object item(Object stack) throws Exception {
      if (stackGetItem == null) {
        stackGetItem = findMethod(stack.getClass(), "getItem", "func_77973_b");
      }
      return stackGetItem.invoke(stack);
    }
  }

  private static boolean isItemStack(Object value) {
    return value != null && "net.minecraft.item.ItemStack".equals(value.getClass().getName());
  }

  private static boolean isEmptyStack(Object value) {
    if (!isItemStack(value)) {
      return value == null;
    }
    try {
      return new Accessors().item(value) == null;
    } catch (Throwable ignored) {
      return true;
    }
  }

  private static int fluidAmount(Object fluidStack) {
    Object amount = fieldValue(fluidStack, "amount");
    if (amount instanceof Number) {
      return ((Number) amount).intValue();
    }
    return 0;
  }

  private static String fluidName(Object fluidStack) throws Exception {
    Object fluid = invokeAny(fluidStack, "getFluid");
    if (fluid == null) {
      return "";
    }
    Object name = invokeAny(fluid, "getName");
    return name != null ? name.toString() : "";
  }

  private static String fluidLocalizedName(Object fluidStack) {
    try {
      Object value = invokeAny(fluidStack, "getLocalizedName");
      return value != null ? value.toString() : "";
    } catch (Throwable ignored) {
      return "";
    }
  }

  private static Object invokeStatic(Class<?> type, String... names) throws Exception {
    Method method = findMethod(type, names);
    return method.invoke(null);
  }

  private static Object invokeAny(Object target, String... names) throws Exception {
    Method method = findMethod(target.getClass(), names);
    return method.invoke(target);
  }

  private static Object firstFieldValue(Object target, String... names) {
    for (int i = 0; i < names.length; i++) {
      Object value = fieldValue(target, names[i]);
      if (value != null) {
        return value;
      }
    }
    return null;
  }

  private static Object fieldValue(Object target, String name) {
    if (target == null) {
      return null;
    }
    try {
      Field field = findField(target.getClass(), name);
      return field.get(target);
    } catch (Throwable ignored) {
      return null;
    }
  }

  private static Integer intField(Object target, String name) {
    Object value = fieldValue(target, name);
    if (value instanceof Number) {
      return Integer.valueOf(((Number) value).intValue());
    }
    return null;
  }

  private static Boolean boolField(Object target, String... names) {
    for (int i = 0; i < names.length; i++) {
      Object value = fieldValue(target, names[i]);
      if (value instanceof Boolean) {
        return (Boolean) value;
      }
    }
    return null;
  }

  private static List<Object> iterable(Object value) {
    if (value == null) {
      return Collections.emptyList();
    }
    if (value instanceof Collection) {
      return new ArrayList<Object>((Collection<?>) value);
    }
    if (value instanceof Map) {
      return new ArrayList<Object>(((Map<?, ?>) value).values());
    }
    Class<?> type = value.getClass();
    if (type.isArray()) {
      int length = Array.getLength(value);
      List<Object> out = new ArrayList<Object>(length);
      for (int i = 0; i < length; i++) {
        out.add(Array.get(value, i));
      }
      return out;
    }
    List<Object> single = new ArrayList<Object>(1);
    single.add(value);
    return single;
  }

  private static Method findMethod(Class<?> type, String... names) {
    Class<?> current = type;
    while (current != null) {
      for (int i = 0; i < names.length; i++) {
        try {
          Method method = current.getDeclaredMethod(names[i]);
          method.setAccessible(true);
          return method;
        } catch (NoSuchMethodException ignored) {
        }
      }
      current = current.getSuperclass();
    }
    throw new RuntimeException("failed to find no-arg method on " + type);
  }

  private static Method findMethod(Class<?> type, String name, Class<?> parameterType) {
    Class<?> current = type;
    while (current != null) {
      Method[] methods = current.getDeclaredMethods();
      for (int i = 0; i < methods.length; i++) {
        Method method = methods[i];
        Class<?>[] parameterTypes = method.getParameterTypes();
        if (!method.getName().equals(name) || parameterTypes.length != 1) {
          continue;
        }
        if (!parameterTypes[0].isAssignableFrom(parameterType)) {
          continue;
        }
        method.setAccessible(true);
        return method;
      }
      current = current.getSuperclass();
    }
    throw new RuntimeException("failed to find method " + name + "(" + parameterType.getName() + ") on " + type);
  }

  private static Field findField(Class<?> type, String... names) {
    Class<?> current = type;
    while (current != null) {
      for (int i = 0; i < names.length; i++) {
        try {
          Field field = current.getDeclaredField(names[i]);
          field.setAccessible(true);
          return field;
        } catch (NoSuchFieldException ignored) {
        }
      }
      current = current.getSuperclass();
    }
    throw new RuntimeException("failed to find field on " + type);
  }

  private static Class<?> classForName(String name) throws ClassNotFoundException {
    ClassLoader loader = Thread.currentThread().getContextClassLoader();
    if (loader != null) {
      return Class.forName(name, false, loader);
    }
    return Class.forName(name);
  }

  private static void setNullableInt(PreparedStatement ps, int index, Integer value) throws SQLException {
    if (value == null) {
      ps.setNull(index, java.sql.Types.INTEGER);
    } else {
      ps.setInt(index, value.intValue());
    }
  }

  private static int generatedId(PreparedStatement ps) throws SQLException {
    ResultSet rs = ps.getGeneratedKeys();
    try {
      if (!rs.next()) {
        throw new SQLException("insert did not return a generated id");
      }
      return rs.getInt(1);
    } finally {
      rs.close();
    }
  }

  private static int selectSingleId(PreparedStatement ps, String label) throws SQLException {
    ResultSet rs = ps.executeQuery();
    try {
      if (!rs.next()) {
        throw new SQLException("failed to select " + label);
      }
      return rs.getInt(1);
    } finally {
      rs.close();
    }
  }

  private static void rollbackQuietly(Connection conn) {
    try {
      conn.rollback();
    } catch (Throwable ignored) {
    }
  }

  private static File getDumpsDir() {
    File configDir = Loader.instance().getConfigDir();
    File baseDir = configDir != null ? configDir.getParentFile() : new File(".");
    File dumpsDir = new File(baseDir, "dumps");
    if (!dumpsDir.exists() && !dumpsDir.mkdirs()) {
      throw new RuntimeException("failed to create dump dir: " + dumpsDir);
    }
    return dumpsDir;
  }

  private static void appendLog(String message) {
    String line = message != null ? message : "";
    System.out.println(line);
    try {
      File logFile = new File(getDumpsDir(), LOG_FILE_NAME);
      BufferedWriter writer =
          java.nio.file.Files.newBufferedWriter(
              logFile.toPath(),
              StandardCharsets.UTF_8,
              java.nio.file.StandardOpenOption.CREATE,
              java.nio.file.StandardOpenOption.APPEND);
      try {
        writer.write(line);
        writer.newLine();
      } finally {
        writer.close();
      }
    } catch (IOException ignored) {
    }
  }

  private static void appendThrowable(String prefix, Throwable throwable) {
    appendLog(prefix + ": " + throwable);
    Throwable current = throwable;
    while (current != null) {
      StackTraceElement[] stack = current.getStackTrace();
      int limit = Math.min(stack.length, 12);
      for (int i = 0; i < limit; i++) {
        appendLog("[GREGGPT]   at " + stack[i]);
      }
      current = current.getCause();
      if (current != null) {
        appendLog("[GREGGPT] caused by " + current);
      }
    }
  }

  private static void sleepQuietly(long millis) {
    try {
      Thread.sleep(millis);
    } catch (InterruptedException ignored) {
    }
  }
}
