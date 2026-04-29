package greggpt.ftbquests;

import com.mojang.brigadier.Command;
import java.io.IOException;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.text.SimpleDateFormat;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.TimeZone;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import net.minecraft.client.Minecraft;
import net.minecraft.commands.Commands;
import net.minecraft.network.chat.Component;
import net.neoforged.bus.api.SubscribeEvent;
import net.neoforged.fml.common.Mod;
import net.neoforged.neoforge.client.event.ClientPlayerNetworkEvent;
import net.neoforged.neoforge.client.event.ClientTickEvent;
import net.neoforged.neoforge.client.event.RegisterClientCommandsEvent;
import net.neoforged.neoforge.common.NeoForge;

@Mod(GregGPTQuestDumpMod.MOD_ID)
public final class GregGPTQuestDumpMod {
  public static final String MOD_ID = "greggpt_quest_dump";
  private static final String MOD_NAME = "GregGPT FTB Quests Dump";
  private static final String VERSION = "1.0.0";
  private static final String SNAPSHOT_FILE = "greggpt_ftbquests_snapshot.json";
  private static final String COMPLETED_FILE = "greggpt_ftbquests_completed.json";
  private static final String LOG_FILE = "greggpt_ftbquests_dump.log";
  private static final long POLL_INTERVAL_MS = 5000L;
  private static final SimpleDateFormat DATE_FORMAT = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSSX");

  private static final String CLIENT_QUEST_FILE = "dev.ftb.mods.ftbquests.client.ClientQuestFile";
  private static final String TEAM_DATA = "dev.ftb.mods.ftbquests.quest.TeamData";

  private final Reflection reflection = new Reflection();
  private long lastPollAtMs;
  private String lastSnapshotDigest = "";
  private boolean waitingForQuestDataLogged;

  static {
    DATE_FORMAT.setTimeZone(TimeZone.getTimeZone("UTC"));
  }

  public GregGPTQuestDumpMod() {
    NeoForge.EVENT_BUS.register(this);
    appendLog("mod loaded");
  }

  @SubscribeEvent
  public void onRegisterClientCommands(RegisterClientCommandsEvent event) {
    event
        .getDispatcher()
        .register(
            Commands.literal("greggptquestsdump")
                .executes(
                    context -> {
                      DumpResult result = dumpNow("command", true);
                      context.getSource().sendSuccess(() -> Component.literal(result.message), false);
                      return result.success ? Command.SINGLE_SUCCESS : 0;
                    }));
  }

  @SubscribeEvent
  public void onClientLogin(ClientPlayerNetworkEvent.LoggingIn event) {
    resetState("client login detected");
  }

  @SubscribeEvent
  public void onClientLogout(ClientPlayerNetworkEvent.LoggingOut event) {
    resetState("client logout detected");
  }

  @SubscribeEvent
  public void onClientTick(ClientTickEvent.Post event) {
    Minecraft minecraft = Minecraft.getInstance();
    if (minecraft.player == null || minecraft.level == null) {
      return;
    }

    long now = System.currentTimeMillis();
    if (now - lastPollAtMs < POLL_INTERVAL_MS) {
      return;
    }
    lastPollAtMs = now;

    DumpResult result = dumpNow("tick", false);
    if (!result.available && !waitingForQuestDataLogged) {
      appendLog("waiting for quest data: " + result.message);
      waitingForQuestDataLogged = true;
    } else if (result.available) {
      waitingForQuestDataLogged = false;
      if (result.wroteFiles) {
        appendLog(result.message);
      }
    }
  }

  private void resetState(String reason) {
    lastPollAtMs = 0L;
    lastSnapshotDigest = "";
    waitingForQuestDataLogged = false;
    appendLog(reason);
  }

  private DumpResult dumpNow(String trigger, boolean forceWrite) {
    Minecraft minecraft = Minecraft.getInstance();
    if (minecraft.player == null) {
      return DumpResult.unavailable("client player unavailable");
    }

    Object questFile = reflection.clientQuestFile();
    if (questFile == null) {
      return DumpResult.unavailable("client quest file not synced yet");
    }

    Object teamData = reflection.fieldValue(questFile, "selfTeamData");
    if (teamData == null) {
      teamData = reflection.invoke(questFile, "getOrCreateTeamData", minecraft.player);
    }
    if (teamData == null) {
      return DumpResult.unavailable("team quest data not available yet");
    }

    UUID playerId = minecraft.player.getUUID();
    Snapshot snapshot = buildSnapshot(questFile, teamData, playerId, trigger);
    String snapshotJson = renderSnapshotJson(snapshot, false);
    String completedJson = renderSnapshotJson(snapshot.onlyCompleted(), true);
    String digest = sha256(snapshotJson);

    if (!forceWrite && digest.equals(lastSnapshotDigest)) {
      return DumpResult.available("quest snapshot unchanged", false, false);
    }

    try {
      Path dumpsDir = minecraft.gameDirectory.toPath().resolve("dumps");
      Files.createDirectories(dumpsDir);
      writeAtomic(dumpsDir.resolve(SNAPSHOT_FILE), snapshotJson);
      writeAtomic(dumpsDir.resolve(COMPLETED_FILE), completedJson);
    } catch (IOException e) {
      appendLog("dump write failed: " + e);
      return DumpResult.available("failed to write quest dump: " + e.getMessage(), false, false);
    }

    lastSnapshotDigest = digest;
    return DumpResult.available(
        "wrote FTB Quests dump for "
            + snapshot.teamName
            + " ("
            + snapshot.completedQuestCount
            + "/"
            + snapshot.questCount
            + " quests completed)",
        true,
        true);
  }

  private Snapshot buildSnapshot(Object questFile, Object teamData, UUID playerId, String trigger) {
    List<Object> chapters = reflection.asObjectList(reflection.invoke(questFile, "getAllChapters"));
    List<ChapterSnapshot> chapterSnapshots = new ArrayList<>();
    int questCount = 0;
    int completedQuestCount = 0;

    for (Object chapter : chapters) {
      List<Object> quests = reflection.asObjectList(reflection.invoke(chapter, "getQuests"));
      quests.sort(
          Comparator.comparingDouble(reflection::questY)
              .thenComparingDouble(reflection::questX)
              .thenComparingLong(reflection::id));

      List<QuestSnapshot> questSnapshots = new ArrayList<>();
      for (Object quest : quests) {
        QuestSnapshot questSnapshot = buildQuestSnapshot(teamData, playerId, quest);
        questSnapshots.add(questSnapshot);
        questCount++;
        if (questSnapshot.completed) {
          completedQuestCount++;
        }
      }

      chapterSnapshots.add(
          new ChapterSnapshot(
              reflection.id(chapter),
              displayTitle(chapter, List.of()),
              sanitize(reflection.stringValue(reflection.invoke(chapter, "getFilename"))),
              reflection.boolValue(reflection.invoke(chapter, "isVisible", teamData)),
              questSnapshots));
    }

    return new Snapshot(
        trigger,
        VERSION,
        Instant.now().toString(),
        reflection.uuidValue(reflection.invoke(teamData, "getTeamId")),
        sanitize(reflection.stringValue(reflection.invoke(teamData, "getName"))),
        sanitize(minecraftServerName()),
        sanitize(playerId.toString()),
        questCount,
        completedQuestCount,
        chapterSnapshots);
  }

  private QuestSnapshot buildQuestSnapshot(Object teamData, UUID playerId, Object quest) {
    List<Object> taskObjects = reflection.asObjectList(reflection.invoke(quest, "getTasksAsList"));
    taskObjects.sort(Comparator.comparingLong(reflection::id));

    List<TaskSnapshot> tasks = new ArrayList<>();
    List<String> taskTitleFallbacks = new ArrayList<>();
    for (Object task : taskObjects) {
      String taskTitle = displayTitle(task, List.of());
      if (!taskTitle.isEmpty() && !looksLikeHexId(taskTitle)) {
        taskTitleFallbacks.add(taskTitle);
      }

      long progress = reflection.longValue(reflection.invoke(teamData, "getProgress", task));
      long maxProgress = reflection.longValue(reflection.invoke(task, "getMaxProgress"));
      tasks.add(
          new TaskSnapshot(
              reflection.id(task),
              taskTitle,
              sanitize(String.valueOf(reflection.invoke(task, "getType"))),
              progress,
              maxProgress,
              maxProgress > 0L && progress >= maxProgress));
    }

    List<Object> rewardObjects = reflection.asObjectList(reflection.invoke(quest, "getRewards"));
    rewardObjects.sort(Comparator.comparingLong(reflection::id));

    List<RewardSnapshot> rewards = new ArrayList<>();
    for (Object reward : rewardObjects) {
      rewards.add(
          new RewardSnapshot(
              reflection.id(reward),
              displayTitle(reward, List.of()),
              sanitize(String.valueOf(reflection.invoke(reward, "getType"))),
              reflection.boolValue(reflection.invoke(teamData, "isRewardClaimed", playerId, reward))));
    }

    return new QuestSnapshot(
        reflection.id(quest),
        displayTitle(quest, taskTitleFallbacks),
        sanitize(reflection.stringValue(reflection.invoke(quest, "getShape"))),
        reflection.doubleValue(reflection.invoke(quest, "getX")),
        reflection.doubleValue(reflection.invoke(quest, "getY")),
        reflection.boolValue(reflection.invoke(quest, "isVisible", teamData)),
        reflection.boolValue(reflection.invoke(teamData, "isStarted", quest)),
        reflection.boolValue(reflection.invoke(teamData, "isCompleted", quest)),
        reflection.boolValue(reflection.invoke(teamData, "canStartTasks", quest)),
        reflection.boolValue(reflection.invoke(teamData, "areDependenciesComplete", quest)),
        reflection.boolValue(reflection.invoke(teamData, "areDependenciesVisible", quest)),
        reflection.boolValue(reflection.invoke(teamData, "hasUnclaimedRewards", playerId, quest)),
        formatDate(reflection.optionalDateValue(reflection.invoke(teamData, "getStartedTime", reflection.id(quest)))),
        formatDate(
            reflection.optionalDateValue(reflection.invoke(teamData, "getCompletedTime", reflection.id(quest)))),
        tasks,
        rewards);
  }

  private String displayTitle(Object object, List<String> taskFallbacks) {
    String title = sanitize(reflection.componentString(reflection.invoke(object, "getTitle")));
    if (!title.isEmpty() && !looksLikeHexId(title)) {
      return title;
    }

    if (taskFallbacks.size() == 1) {
      return taskFallbacks.getFirst();
    } else if (!taskFallbacks.isEmpty()) {
      return taskFallbacks.getFirst() + " +" + (taskFallbacks.size() - 1) + " tasks";
    }

    String rawTitle = sanitize(reflection.stringValue(reflection.invoke(object, "getRawTitle")));
    if (!rawTitle.isEmpty() && !looksLikeHexId(rawTitle)) {
      return rawTitle;
    }

    return sanitize(reflection.stringValue(reflection.invoke(object, "getCodeString")));
  }

  private static boolean looksLikeHexId(String value) {
    if (value.length() != 16) {
      return false;
    }
    for (int i = 0; i < value.length(); i++) {
      char c = value.charAt(i);
      if (!((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F'))) {
        return false;
      }
    }
    return true;
  }

  private static String minecraftServerName() {
    Minecraft minecraft = Minecraft.getInstance();
    if (minecraft.getCurrentServer() != null && minecraft.getCurrentServer().name != null) {
      return minecraft.getCurrentServer().name;
    }
    if (minecraft.hasSingleplayerServer()) {
      return "singleplayer";
    }
    return "";
  }

  private static String formatDate(Optional<Date> date) {
    return date.map(DATE_FORMAT::format).orElse("");
  }

  private static void writeAtomic(Path target, String contents) throws IOException {
    Path temp = target.resolveSibling(target.getFileName() + ".tmp");
    Files.writeString(temp, contents, StandardCharsets.UTF_8);
    try {
      Files.move(temp, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
    } catch (IOException ignored) {
      Files.move(temp, target, StandardCopyOption.REPLACE_EXISTING);
    }
  }

  private static String sha256(String input) {
    try {
      MessageDigest digest = MessageDigest.getInstance("SHA-256");
      byte[] bytes = digest.digest(input.getBytes(StandardCharsets.UTF_8));
      StringBuilder out = new StringBuilder(bytes.length * 2);
      for (byte b : bytes) {
        out.append(Character.forDigit((b >>> 4) & 0xF, 16));
        out.append(Character.forDigit(b & 0xF, 16));
      }
      return out.toString();
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException("missing SHA-256", e);
    }
  }

  private static String sanitize(String value) {
    return value == null ? "" : value.replace('\r', ' ').replace('\n', ' ').trim();
  }

  private static void appendLog(String message) {
    Minecraft minecraft = Minecraft.getInstance();
    Path root =
        minecraft != null && minecraft.gameDirectory != null
            ? minecraft.gameDirectory.toPath()
            : Path.of(".");
    Path dumpsDir = root.resolve("dumps");
    try {
      Files.createDirectories(dumpsDir);
      Files.writeString(
          dumpsDir.resolve(LOG_FILE),
          "[" + Instant.now() + "] " + message + System.lineSeparator(),
          StandardCharsets.UTF_8,
          java.nio.file.StandardOpenOption.CREATE,
          java.nio.file.StandardOpenOption.APPEND);
    } catch (IOException ignored) {
    }
    System.out.println("[" + MOD_NAME + "] " + message);
  }

  private static String renderSnapshotJson(Snapshot snapshot, boolean completedOnly) {
    StringBuilder out = new StringBuilder(65536);
    out.append("{\n");
    appendField(out, 1, "mod_id", MOD_ID, true);
    appendField(out, 1, "version", snapshot.version, true);
    appendField(out, 1, "trigger", snapshot.trigger, true);
    appendField(out, 1, "generated_at", snapshot.generatedAt, true);
    appendField(out, 1, "server_name", snapshot.serverName, true);
    appendField(out, 1, "team_id", snapshot.teamId.toString(), true);
    appendField(out, 1, "team_name", snapshot.teamName, true);
    appendField(out, 1, "player_id", snapshot.playerId, true);
    appendNumberField(out, 1, "quest_count", snapshot.questCount, true);
    appendNumberField(out, 1, "completed_quest_count", snapshot.completedQuestCount, true);
    appendField(out, 1, "mode", completedOnly ? "completed_only" : "full", true);
    indent(out, 1).append("\"chapters\": [\n");

    for (int i = 0; i < snapshot.chapters.size(); i++) {
      ChapterSnapshot chapter = snapshot.chapters.get(i);
      indent(out, 2).append("{\n");
      appendNumberField(out, 3, "id", chapter.id, true);
      appendField(out, 3, "title", chapter.title, true);
      appendField(out, 3, "filename", chapter.filename, true);
      appendBooleanField(out, 3, "visible", chapter.visible, true);
      appendNumberField(out, 3, "quest_count", chapter.quests.size(), true);
      indent(out, 3).append("\"quests\": [\n");

      for (int j = 0; j < chapter.quests.size(); j++) {
        QuestSnapshot quest = chapter.quests.get(j);
        indent(out, 4).append("{\n");
        appendNumberField(out, 5, "id", quest.id, true);
        appendField(out, 5, "title", quest.title, true);
        appendField(out, 5, "shape", quest.shape, true);
        appendNumberField(out, 5, "x", quest.x, true);
        appendNumberField(out, 5, "y", quest.y, true);
        appendBooleanField(out, 5, "visible", quest.visible, true);
        appendBooleanField(out, 5, "started", quest.started, true);
        appendBooleanField(out, 5, "completed", quest.completed, true);
        appendBooleanField(out, 5, "can_start", quest.canStart, true);
        appendBooleanField(out, 5, "dependencies_complete", quest.dependenciesComplete, true);
        appendBooleanField(out, 5, "dependencies_visible", quest.dependenciesVisible, true);
        appendBooleanField(out, 5, "has_unclaimed_rewards", quest.hasUnclaimedRewards, true);
        appendField(out, 5, "started_at", quest.startedAt, true);
        appendField(out, 5, "completed_at", quest.completedAt, true);
        indent(out, 5).append("\"tasks\": [\n");

        for (int k = 0; k < quest.tasks.size(); k++) {
          TaskSnapshot task = quest.tasks.get(k);
          indent(out, 6).append("{\n");
          appendNumberField(out, 7, "id", task.id, true);
          appendField(out, 7, "title", task.title, true);
          appendField(out, 7, "type", task.type, true);
          appendNumberField(out, 7, "progress", task.progress, true);
          appendNumberField(out, 7, "max_progress", task.maxProgress, true);
          appendBooleanField(out, 7, "completed", task.completed, false);
          indent(out, 6).append("}");
          if (k + 1 < quest.tasks.size()) {
            out.append(',');
          }
          out.append('\n');
        }

        indent(out, 5).append("],\n");
        indent(out, 5).append("\"rewards\": [\n");

        for (int k = 0; k < quest.rewards.size(); k++) {
          RewardSnapshot reward = quest.rewards.get(k);
          indent(out, 6).append("{\n");
          appendNumberField(out, 7, "id", reward.id, true);
          appendField(out, 7, "title", reward.title, true);
          appendField(out, 7, "type", reward.type, true);
          appendBooleanField(out, 7, "claimed", reward.claimed, false);
          indent(out, 6).append("}");
          if (k + 1 < quest.rewards.size()) {
            out.append(',');
          }
          out.append('\n');
        }

        indent(out, 5).append("]\n");
        indent(out, 4).append("}");
        if (j + 1 < chapter.quests.size()) {
          out.append(',');
        }
        out.append('\n');
      }

      indent(out, 3).append("]\n");
      indent(out, 2).append("}");
      if (i + 1 < snapshot.chapters.size()) {
        out.append(',');
      }
      out.append('\n');
    }

    indent(out, 1).append("]\n");
    out.append("}\n");
    return out.toString();
  }

  private static StringBuilder indent(StringBuilder out, int level) {
    for (int i = 0; i < level; i++) {
      out.append("  ");
    }
    return out;
  }

  private static void appendField(
      StringBuilder out, int level, String name, String value, boolean trailingComma) {
    indent(out, level)
        .append('"')
        .append(escape(name))
        .append("\": ")
        .append('"')
        .append(escape(value))
        .append('"');
    if (trailingComma) {
      out.append(',');
    }
    out.append('\n');
  }

  private static void appendBooleanField(
      StringBuilder out, int level, String name, boolean value, boolean trailingComma) {
    indent(out, level).append('"').append(escape(name)).append("\": ").append(value);
    if (trailingComma) {
      out.append(',');
    }
    out.append('\n');
  }

  private static void appendNumberField(
      StringBuilder out, int level, String name, double value, boolean trailingComma) {
    String rendered =
        Math.rint(value) == value
            ? Long.toString((long) value)
            : String.format(Locale.ROOT, "%.3f", value).replaceAll("0+$", "").replaceAll("\\.$", "");
    indent(out, level).append('"').append(escape(name)).append("\": ").append(rendered);
    if (trailingComma) {
      out.append(',');
    }
    out.append('\n');
  }

  private static void appendNumberField(
      StringBuilder out, int level, String name, long value, boolean trailingComma) {
    indent(out, level).append('"').append(escape(name)).append("\": ").append(value);
    if (trailingComma) {
      out.append(',');
    }
    out.append('\n');
  }

  private static String escape(String value) {
    StringBuilder out = new StringBuilder(value.length() + 16);
    for (int i = 0; i < value.length(); i++) {
      char c = value.charAt(i);
      switch (c) {
        case '\\':
          out.append("\\\\");
          break;
        case '"':
          out.append("\\\"");
          break;
        case '\b':
          out.append("\\b");
          break;
        case '\f':
          out.append("\\f");
          break;
        case '\n':
          out.append("\\n");
          break;
        case '\r':
          out.append("\\r");
          break;
        case '\t':
          out.append("\\t");
          break;
        default:
          if (c < 0x20) {
            out.append(String.format(Locale.ROOT, "\\u%04x", (int) c));
          } else {
            out.append(c);
          }
      }
    }
    return out.toString();
  }

  private record DumpResult(boolean available, boolean success, boolean wroteFiles, String message) {
    private static DumpResult unavailable(String message) {
      return new DumpResult(false, false, false, message);
    }

    private static DumpResult available(String message, boolean success, boolean wroteFiles) {
      return new DumpResult(true, success, wroteFiles, message);
    }
  }

  private record Snapshot(
      String trigger,
      String version,
      String generatedAt,
      UUID teamId,
      String teamName,
      String serverName,
      String playerId,
      int questCount,
      int completedQuestCount,
      List<ChapterSnapshot> chapters) {
    private Snapshot onlyCompleted() {
      List<ChapterSnapshot> filtered = new ArrayList<>();
      for (ChapterSnapshot chapter : chapters) {
        List<QuestSnapshot> completed = new ArrayList<>();
        for (QuestSnapshot quest : chapter.quests) {
          if (quest.completed) {
            completed.add(quest);
          }
        }
        if (!completed.isEmpty()) {
          filtered.add(
              new ChapterSnapshot(chapter.id, chapter.title, chapter.filename, chapter.visible, completed));
        }
      }
      return new Snapshot(
          trigger,
          version,
          generatedAt,
          teamId,
          teamName,
          serverName,
          playerId,
          questCount,
          completedQuestCount,
          filtered);
    }
  }

  private record ChapterSnapshot(
      long id, String title, String filename, boolean visible, List<QuestSnapshot> quests) {}

  private record QuestSnapshot(
      long id,
      String title,
      String shape,
      double x,
      double y,
      boolean visible,
      boolean started,
      boolean completed,
      boolean canStart,
      boolean dependenciesComplete,
      boolean dependenciesVisible,
      boolean hasUnclaimedRewards,
      String startedAt,
      String completedAt,
      List<TaskSnapshot> tasks,
      List<RewardSnapshot> rewards) {}

  private record TaskSnapshot(
      long id, String title, String type, long progress, long maxProgress, boolean completed) {}

  private record RewardSnapshot(long id, String title, String type, boolean claimed) {}

  private static final class Reflection {
    private final Map<String, Class<?>> classes = new ConcurrentHashMap<>();
    private final Map<String, Method> methods = new ConcurrentHashMap<>();
    private final Map<String, Field> fields = new ConcurrentHashMap<>();

    private Object clientQuestFile() {
      Boolean exists = boolObject(invokeStatic(CLIENT_QUEST_FILE, "exists"));
      if (!Boolean.TRUE.equals(exists)) {
        return null;
      }
      return staticFieldValue(CLIENT_QUEST_FILE, "INSTANCE");
    }

    private long id(Object target) {
      return longValue(invoke(target, "getId"));
    }

    private double questX(Object quest) {
      return doubleValue(invoke(quest, "getX"));
    }

    private double questY(Object quest) {
      return doubleValue(invoke(quest, "getY"));
    }

    private Class<?> findClass(String className) {
      return classes.computeIfAbsent(
          className,
          key -> {
            try {
              return Class.forName(key);
            } catch (ClassNotFoundException e) {
              throw new IllegalStateException("missing class " + key, e);
            }
          });
    }

    private Object invokeStatic(String className, String methodName, Object... args) {
      try {
        Class<?> type = findClass(className);
        Method method = findMethod(type, methodName, args);
        return method.invoke(null, args);
      } catch (ReflectiveOperationException | IllegalStateException e) {
        return null;
      }
    }

    private Object staticFieldValue(String className, String fieldName) {
      try {
        Class<?> type = findClass(className);
        Field field = findField(type, fieldName);
        return field.get(null);
      } catch (ReflectiveOperationException | IllegalStateException e) {
        return null;
      }
    }

    private Object fieldValue(Object target, String fieldName) {
      if (target == null) {
        return null;
      }
      try {
        Field field = findField(target.getClass(), fieldName);
        return field.get(target);
      } catch (ReflectiveOperationException e) {
        return null;
      }
    }

    private Object invoke(Object target, String methodName, Object... args) {
      if (target == null) {
        return null;
      }
      try {
        Method method = findMethod(target.getClass(), methodName, args);
        return method.invoke(target, args);
      } catch (ReflectiveOperationException e) {
        return null;
      }
    }

    private Method findMethod(Class<?> type, String methodName, Object[] args) {
      String key = type.getName() + "#" + methodName + "#" + signature(args);
      return methods.computeIfAbsent(
          key,
          ignored -> {
            for (Method method : type.getMethods()) {
              if (!method.getName().equals(methodName)) {
                continue;
              }
              Class<?>[] params = method.getParameterTypes();
              if (params.length != args.length) {
                continue;
              }
              if (matches(params, args)) {
                method.setAccessible(true);
                return method;
              }
            }
            throw new IllegalStateException("missing method " + key);
          });
    }

    private Field findField(Class<?> type, String fieldName) {
      String key = type.getName() + "#" + fieldName;
      return fields.computeIfAbsent(
          key,
          ignored -> {
            Class<?> current = type;
            while (current != null) {
              try {
                Field field = current.getDeclaredField(fieldName);
                field.setAccessible(true);
                return field;
              } catch (NoSuchFieldException ignoredEx) {
                current = current.getSuperclass();
              }
            }
            throw new IllegalStateException("missing field " + key);
          });
    }

    private boolean matches(Class<?>[] params, Object[] args) {
      for (int i = 0; i < params.length; i++) {
        if (args[i] == null) {
          continue;
        }
        Class<?> param = wrap(params[i]);
        Class<?> arg = wrap(args[i].getClass());
        if (!param.isAssignableFrom(arg)) {
          return false;
        }
      }
      return true;
    }

    private Class<?> wrap(Class<?> type) {
      if (!type.isPrimitive()) {
        return type;
      }
      if (type == boolean.class) return Boolean.class;
      if (type == byte.class) return Byte.class;
      if (type == short.class) return Short.class;
      if (type == int.class) return Integer.class;
      if (type == long.class) return Long.class;
      if (type == float.class) return Float.class;
      if (type == double.class) return Double.class;
      if (type == char.class) return Character.class;
      return type;
    }

    private String signature(Object[] args) {
      StringBuilder out = new StringBuilder();
      for (Object arg : args) {
        out.append(arg == null ? "null" : wrap(arg.getClass()).getName()).append(';');
      }
      return out.toString();
    }

    @SuppressWarnings("unchecked")
    private List<Object> asObjectList(Object value) {
      if (value instanceof List<?>) {
        return new ArrayList<>((List<Object>) value);
      } else if (value instanceof Iterable<?> iterable) {
        List<Object> out = new ArrayList<>();
        iterable.forEach(out::add);
        return out;
      }
      return new ArrayList<>();
    }

    private String stringValue(Object value) {
      return value == null ? "" : String.valueOf(value);
    }

    private String componentString(Object value) {
      return value instanceof Component component ? component.getString() : stringValue(value);
    }

    private boolean boolValue(Object value) {
      return value instanceof Boolean b && b;
    }

    private Boolean boolObject(Object value) {
      return value instanceof Boolean b ? b : null;
    }

    private long longValue(Object value) {
      return value instanceof Number n ? n.longValue() : 0L;
    }

    private double doubleValue(Object value) {
      return value instanceof Number n ? n.doubleValue() : 0D;
    }

    private UUID uuidValue(Object value) {
      return value instanceof UUID uuid ? uuid : new UUID(0L, 0L);
    }

    @SuppressWarnings("unchecked")
    private Optional<Date> optionalDateValue(Object value) {
      return value instanceof Optional<?> optional && optional.orElse(null) instanceof Date date
          ? Optional.of(date)
          : Optional.empty();
    }
  }
}
