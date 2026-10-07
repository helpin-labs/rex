import {
  Bike, BookOpen, Bot, Briefcase, Bug, CalendarDays, Calculator, Camera, Car, Cat, ChartColumn, ChartCandlestick, ChefHat, Clapperboard,
  Code, Coffee, Coins, Compass, CreditCard, Database, Dog, Dumbbell, Earth, Feather, FlaskConical, Flower2, FolderOpen, Footprints,
  Gamepad2, Gift, Globe, GraduationCap, Hammer, Handshake, Headphones, Heart, Hotel, House, Landmark, Languages, Leaf, Lightbulb, ListChecks,
  Lock, Luggage, type LucideIcon, Mail, Map, Megaphone, Microscope, Moon, Mountain, Newspaper, Palette, Palmtree, PawPrint, PenLine,
  Pill, Plane, Receipt, Rocket, Salad, Scale, Search, ShoppingCart, Smartphone, Sofa, Sparkles, Sprout, Star, Stethoscope, Sun, Target,
  TrainFront, TrendingUp, Users, Wallet, Wand, Wine, Workflow, Wrench, Brain,
} from "lucide-react";

// Bot avatars: a curated set of monochrome Lucide icons with keywords, used
// for search and to suggest an icon from the bot's name. A bot stores the
// icon's id; bots saved with an emoji map to the icon that replaced it.

export type IconEntry = { id: string; Icon: LucideIcon; emoji?: string; keywords: string[] };
export type IconGroup = { name: string; items: IconEntry[] };

const i = (id: string, Icon: LucideIcon, keywords: string, emoji?: string): IconEntry => ({ id, Icon, emoji, keywords: keywords.split(" ") });

export const iconGroups: IconGroup[] = [
  { name: "Assistants", items: [
    i("bot", Bot, "bot robot assistant ai agent general helper", "🤖"), i("compass", Compass, "guide compass navigator assistant general direction", "🧭"),
    i("sparkles", Sparkles, "magic smart ai assistant spark", "✨"), i("wand", Wand, "genie wish helper concierge magic", "🧞"),
    i("brain", Brain, "brain think memory research analyst knowledge", "🧠"), i("lightbulb", Lightbulb, "idea ideas brainstorm innovation creative", "💡"),
    i("target", Target, "goal focus coach target okr strategy", "🎯"), i("rocket", Rocket, "launch startup founder ship productivity"),
  ] },
  { name: "Work", items: [
    i("pen-line", PenLine, "writer write notes scribe editor draft document", "📝"), i("feather", Feather, "writer author copywriter blog content poetry", "✍️"),
    i("list-checks", ListChecks, "tasks todo checklist planner project manager", "📋"), i("calendar", CalendarDays, "calendar schedule meetings planner agenda organizer", "📅"),
    i("mail", Mail, "email mail inbox newsletter outreach", "📧"), i("briefcase", Briefcase, "work business office career job recruiter", "💼"),
    i("chart", ChartColumn, "data analyst analytics chart dashboard report extractor stats", "📊"), i("trending-up", TrendingUp, "growth sales marketing metrics trends", "📈"),
    i("folder", FolderOpen, "files organize archive secretary admin documents", "🗂️"), i("search", Search, "research researcher search investigate find detective", "🔎"),
    i("scale", Scale, "legal law lawyer contract compliance", "⚖️"), i("receipt", Receipt, "invoice receipt expenses accounting bookkeeping tax", "🧾"),
    i("megaphone", Megaphone, "marketing social media announcement pr campaign", "📣"), i("handshake", Handshake, "partner sales crm customer deal negotiation", "🤝"),
    i("languages", Languages, "translate translator language interpreter speaker", "🗣️"),
  ] },
  { name: "Tech", items: [
    i("code", Code, "code coder developer programmer engineer software", "💻"), i("wrench", Wrench, "tools builder fix maintenance devops repair", "🛠️"),
    i("workflow", Workflow, "automation ops system workflow pipeline", "⚙️"), i("bug", Bug, "debug bug tester qa test", "🐛"),
    i("lock", Lock, "security password privacy secure", "🔐"), i("globe", Globe, "web browser internet website seo", "🌐"),
    i("database", Database, "database data storage sql", "🗄️"), i("smartphone", Smartphone, "mobile app phone ios android", "📱"),
    i("flask", FlaskConical, "experiment science lab test chemistry", "🧪"),
  ] },
  { name: "Travel", items: [
    i("plane", Plane, "travel trip flight flights vacation holiday airport journey spain abroad", "✈️"), i("luggage", Luggage, "packing luggage trip travel", "🧳"),
    i("map", Map, "map maps route itinerary explore", "🗺️"), i("palmtree", Palmtree, "beach holiday vacation summer island", "🏖️"),
    i("mountain", Mountain, "mountain hiking ski outdoors", "🏔️"), i("train", TrainFront, "train rail commute transit", "🚆"),
    i("car", Car, "car drive road trip parking", "🚗"), i("hotel", Hotel, "hotel booking stay accommodation", "🏨"),
    i("earth", Earth, "world global international europe", "🌍"),
  ] },
  { name: "Food", items: [
    i("chef-hat", ChefHat, "chef cook cooking recipe recipes kitchen meal", "🧑‍🍳"), i("salad", Salad, "diet nutrition healthy salad meal plan", "🥗"),
    i("coffee", Coffee, "coffee cafe morning breakfast", "☕"), i("wine", Wine, "wine dinner restaurant", "🍷"),
    i("shopping-cart", ShoppingCart, "shopping groceries grocery list store", "🛒"),
  ] },
  { name: "Home & life", items: [
    i("house", House, "home house household chores", "🏠"), i("sprout", Sprout, "garden plants growth habit", "🌱"),
    i("users", Users, "family kids parenting children people friends", "👨‍👩‍👧"), i("gift", Gift, "gift gifts birthday present", "🎁"),
    i("dog", Dog, "dog pet pets puppy", "🐶"), i("cat", Cat, "cat pet pets kitten", "🐱"),
    i("hammer", Hammer, "diy handyman build fix", "🔧"), i("sofa", Sofa, "interior decor furniture", "🛋️"),
    i("heart", Heart, "love relationship dating care"),
  ] },
  { name: "Health", items: [
    i("dumbbell", Dumbbell, "fitness gym workout trainer exercise strength", "💪"), i("footprints", Footprints, "running runner walking steps cardio marathon", "🏃"),
    i("flower", Flower2, "meditation mindfulness yoga calm wellbeing", "🧘"), i("stethoscope", Stethoscope, "health doctor medical symptoms care", "🩺"),
    i("moon", Moon, "sleep rest recovery night evening", "😴"), i("pill", Pill, "medication pills pharmacy reminder", "💊"),
    i("bike", Bike, "cycling bike ride"),
  ] },
  { name: "Money", items: [
    i("wallet", Wallet, "money finance budget savings wealth", "💰"), i("credit-card", CreditCard, "payments card spending", "💳"),
    i("landmark", Landmark, "bank banking loans mortgage", "🏦"), i("candlestick", ChartCandlestick, "stocks invest investing trading market crypto", "📉"),
    i("coins", Coins, "crypto coin coins savings", "🪙"),
  ] },
  { name: "Learning", items: [
    i("book", BookOpen, "learn study books reading library teacher tutor", "📚"), i("graduation-cap", GraduationCap, "school university course exam student education", "🎓"),
    i("calculator", Calculator, "math maths calculate homework", "🧮"), i("microscope", Microscope, "science research biology", "🔬"),
    i("newspaper", Newspaper, "news digest briefing headlines summary", "📰"),
  ] },
  { name: "Creative", items: [
    i("palette", Palette, "art design designer creative paint illustration", "🎨"), i("camera", Camera, "photo photography camera images", "📸"),
    i("clapperboard", Clapperboard, "video film movie youtube editor", "🎬"), i("headphones", Headphones, "music podcast audio playlist sound", "🎧"),
    i("gamepad", Gamepad2, "games gaming gamer", "🎮"),
  ] },
  { name: "Nature", items: [
    i("paw", PawPrint, "animal animals wildlife", "🦊"), i("leaf", Leaf, "nature eco green environment"),
    i("star", Star, "star favorite", "🌟"), i("sun", Sun, "sun morning day weather", "☀️"),
  ] },
];

export const allIcons: IconEntry[] = iconGroups.flatMap(group => group.items);
const byId = new globalThis.Map(allIcons.map(entry => [entry.id, entry]));
const byEmoji = new globalThis.Map(allIcons.filter(entry => entry.emoji).map(entry => [entry.emoji!, entry]));

/** The icon id for a stored value: an icon id, or an emoji saved before icons. */
export function resolveIconId(value: string): string {
  return (byId.get(value) || byEmoji.get(value))?.id || "bot";
}

/** The icon for a stored value: an icon id, or an emoji saved before icons. */
export function resolveIcon(value: string): LucideIcon {
  return (byId.get(value) || byEmoji.get(value))?.Icon || Bot;
}

function words(text: string): string[] {
  return text.toLowerCase().normalize("NFKD").replace(/[^a-z0-9 ]+/g, " ").split(/\s+/).filter(word => word.length > 1);
}

/** Icon ids that fit a bot's name (and instructions), best first. */
export function suggestIcons(name: string, instructions = "", limit = 6): string[] {
  const nameWords = words(name);
  const instructionWords = words(instructions).slice(0, 60);
  if (!nameWords.length && !instructionWords.length) return [];
  const scored = allIcons.map(entry => {
    let score = 0;
    for (const keyword of entry.keywords) {
      for (const word of nameWords) {
        if (word === keyword) score += 6;
        else if (word.length >= 3 && (keyword.startsWith(word) || word.startsWith(keyword))) score += 3;
      }
      for (const word of instructionWords) if (word === keyword) score += 1;
    }
    return { id: entry.id, score };
  }).filter(item => item.score > 0);
  scored.sort((a, b) => b.score - a.score);
  return scored.slice(0, limit).map(item => item.id);
}

/** Icon ids whose keywords match a search query. */
export function searchIcons(query: string): string[] {
  const terms = words(query);
  if (!terms.length) return allIcons.map(entry => entry.id);
  return allIcons.filter(entry => terms.every(term => entry.id.startsWith(term) || entry.keywords.some(keyword => keyword.startsWith(term)))).map(entry => entry.id);
}
